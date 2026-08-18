package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/auth"
	"github.com/apdsoftware/postqron/services/api/internal/config"
	"github.com/apdsoftware/postqron/services/api/internal/httpapi"
	"github.com/apdsoftware/postqron/services/api/internal/legal"
)

// ---------------------------------------------------------------- impalcatura

// consensiInMemoria è quel poco di store che serve alle rotte. Le proprietà che
// dipendono da PostgreSQL — l'immutabilità della prova, l'unicità per
// (documento, versione), la cascata alla cancellazione — hanno i loro test in
// internal/legalpg contro il database vero; qui si verifica il contratto HTTP.
type consensiInMemoria struct {
	righe []legal.Consent
}

func (s *consensiInMemoria) ConsentsOf(context.Context, string) ([]legal.Consent, error) {
	return append([]legal.Consent(nil), s.righe...), nil
}

func (s *consensiInMemoria) Record(_ context.Context, _ string, consents []legal.Consent) error {
	for _, c := range consents {
		presente := false
		for _, esistente := range s.righe {
			if esistente.Document == c.Document && esistente.Version == c.Version {
				presente = true
				break
			}
		}
		if !presente {
			s.righe = append(s.righe, c)
		}
	}
	return nil
}

type legalFixture struct {
	*api
	store *consensiInMemoria
	token string
}

func newLegalFixture(t *testing.T) *legalFixture {
	t.Helper()

	store := &consensiInMemoria{}
	a := newAPI(t, func(_ *config.Config, _ *auth.Options, deps *httpapi.Deps) {
		svc, err := legal.New(legal.Options{Store: store})
		if err != nil {
			t.Fatalf("legal.New: %v", err)
		}
		deps.Legal = svc
	})
	_, token := a.registerAndLogin()
	return &legalFixture{api: a, store: store, token: token}
}

func statoConsensi(t *testing.T, rec interface{ Bytes() []byte }) httpapi.LegalConsentsResponse {
	t.Helper()
	var body httpapi.LegalConsentsResponse
	if err := json.Unmarshal(rec.Bytes(), &body); err != nil {
		t.Fatalf("decodifica della risposta: %v", err)
	}
	return body
}

// ------------------------------------------------------------------- rotte

// TestIConsensiSiLeggonoConVersioneELingua è R46 visto dall'API: le tre cose che
// la prova deve dire ci sono tutte, per ciascuno dei quattro documenti.
func TestIConsensiSiLeggonoConVersioneELingua(t *testing.T) {
	f := newLegalFixture(t)

	// La registrazione ha già scritto i consensi: lo store delle rotte è però un
	// altro (quello dell'autenticazione è finto e non li propaga), quindi qui si
	// parte dall'accettazione esplicita — che è anche il caso di chi ha visto
	// cambiare un documento.
	rec := f.do(http.MethodGet, "/legal/consents", nil, withCookie(f.token))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /legal/consents = %d: %s", rec.Code, rec.Body)
	}
	stato := statoConsensi(t, rec.Body)
	if len(stato.Accepted) != 0 {
		t.Errorf("consensi già presenti su un archivio vuoto: %+v", stato.Accepted)
	}
	if len(stato.Outstanding) != len(legal.Documents()) {
		t.Fatalf("documenti da accettare = %d, attesi %d", len(stato.Outstanding), len(legal.Documents()))
	}

	da := make([]map[string]string, 0, len(stato.Outstanding))
	for _, r := range stato.Outstanding {
		if r.Version == "" || r.Language == "" || r.EffectiveDate.IsZero() {
			t.Errorf("%s: la richiesta di accettazione non dice versione, lingua o data: %+v", r.Document, r)
		}
		if r.AcceptedVersion != "" {
			t.Errorf("%s: dichiara una versione accettata (%q) che non esiste", r.Document, r.AcceptedVersion)
		}
		da = append(da, map[string]string{"document": r.Document, "version": r.Version})
	}

	rec = f.do(http.MethodPost, "/legal/consents",
		map[string]any{"language": "it", "documents": da}, withCookie(f.token))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /legal/consents = %d: %s", rec.Code, rec.Body)
	}
	stato = statoConsensi(t, rec.Body)
	if len(stato.Outstanding) != 0 {
		t.Errorf("dopo l'accettazione restano %d documenti in sospeso: %+v", len(stato.Outstanding), stato.Outstanding)
	}
	if len(stato.Accepted) != len(legal.Documents()) {
		t.Fatalf("consensi registrati = %d, attesi %d", len(stato.Accepted), len(legal.Documents()))
	}

	versioni := map[string]string{}
	for _, c := range stato.Accepted {
		versioni[c.Document] = c.Version
		if c.Language == "" || c.DocumentChecksum == "" || c.AcceptedAt.IsZero() {
			t.Errorf("%s: la prova non dice lingua, impronta o data: %+v", c.Document, c)
		}
		if c.Source != string(legal.SourceReacceptance) {
			t.Errorf("%s: origine %q, attesa %q", c.Document, c.Source, legal.SourceReacceptance)
		}
	}
	// Le quattro versioni non sono uguali: è la ragione per cui la risposta ha
	// una voce per documento invece di un numero solo.
	if versioni["terms-of-service"] == versioni["cookie-policy"] {
		t.Errorf("Termini e cookie policy alla stessa versione (%s): quattro documenti appiattiti in uno",
			versioni["terms-of-service"])
	}
}

// TestNonSiPuòAccettareUnaVersioneDiversaDaQuellaInVigore copre la corsa fra la
// pagina che si carica e il bottone che si preme.
func TestNonSiPuòAccettareUnaVersioneDiversaDaQuellaInVigore(t *testing.T) {
	f := newLegalFixture(t)

	rec := f.do(http.MethodPost, "/legal/consents", map[string]any{
		"language":  "en",
		"documents": []map[string]string{{"document": "terms-of-service", "version": "0.9.0"}},
	}, withCookie(f.token))

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, atteso 409: %s", rec.Code, rec.Body)
	}
	if code := errorCode(t, rec); code != "legal_version_not_in_force" {
		t.Errorf("codice = %q, atteso legal_version_not_in_force", code)
	}
	if len(f.store.righe) != 0 {
		t.Errorf("una prova è stata scritta su una versione che non vincola: %+v", f.store.righe)
	}
}

func TestIlCorpoDellAccettazioneVieneValidato(t *testing.T) {
	f := newLegalFixture(t)
	inVigore, _ := legal.Current().InForce(legal.TermsOfService, time.Now())

	casi := map[string]any{
		"elenco vuoto": map[string]any{"language": "en", "documents": []map[string]string{}},
		"documento sconosciuto": map[string]any{
			"language":  "en",
			"documents": []map[string]string{{"document": "marketing-consent", "version": "1.0.0"}},
		},
		"versione mancante": map[string]any{
			"language":  "en",
			"documents": []map[string]string{{"document": "terms-of-service"}},
		},
		"lingua non supportata": map[string]any{
			"language":  "pt",
			"documents": []map[string]string{{"document": "terms-of-service", "version": inVigore.Version}},
		},
		"documento ripetuto": map[string]any{
			"language": "en",
			"documents": []map[string]string{
				{"document": "terms-of-service", "version": inVigore.Version},
				{"document": "terms-of-service", "version": inVigore.Version},
			},
		},
	}
	for nome, corpo := range casi {
		t.Run(nome, func(t *testing.T) {
			rec := f.do(http.MethodPost, "/legal/consents", corpo, withCookie(f.token))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, atteso 400: %s", rec.Code, rec.Body)
			}
			if code := errorCode(t, rec); code != "validation_failed" {
				t.Errorf("codice = %q, atteso validation_failed", code)
			}
		})
	}
	if len(f.store.righe) != 0 {
		t.Errorf("una prova è stata scritta da una richiesta non valida: %+v", f.store.righe)
	}
}

// TestUnaChiaveAPINonPuòAccettareIDocumentiLegali: accettare un contratto è un
// atto della persona, e una credenziale di servizio dimenticata in un file di
// configurazione non deve poter vincolare nessuno.
func TestUnaChiaveAPINonPuòAccettareIDocumentiLegali(t *testing.T) {
	f := newLegalFixture(t)

	rec := f.do(http.MethodPost, "/keys", map[string]any{
		"name":   "chiave-di-servizio",
		"scopes": []string{"jobs:write", "jobs:read"},
	}, withCookie(f.token))
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /keys = %d: %s", rec.Code, rec.Body)
	}
	var creata httpapi.APIKeyCreatedResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &creata); err != nil {
		t.Fatalf("decodifica: %v", err)
	}

	inVigore, _ := legal.Current().InForce(legal.TermsOfService, time.Now())
	casi := []struct{ metodo, percorso string }{
		{http.MethodGet, "/legal/consents"},
		{http.MethodPost, "/legal/consents"},
	}
	for _, caso := range casi {
		rec := f.do(caso.metodo, caso.percorso, map[string]any{
			"language":  "en",
			"documents": []map[string]string{{"document": "terms-of-service", "version": inVigore.Version}},
		}, withKey(creata.Secret))
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s con una chiave API = %d, atteso 401: %s",
				caso.metodo, caso.percorso, rec.Code, rec.Body)
		}
	}
	if len(f.store.righe) != 0 {
		t.Errorf("una chiave API ha registrato un consenso: %+v", f.store.righe)
	}
}

// TestLaLinguaChiestaNonCambiaLaLinguaDelConsensoGiàPrestato è la proprietà per
// cui la lingua sta nella riga e non in una preferenza: la prova dice cosa
// l'utente ha letto **allora**.
func TestLaLinguaChiestaNonCambiaLaLinguaDelConsensoGiàPrestato(t *testing.T) {
	f := newLegalFixture(t)
	inVigore, _ := legal.Current().InForce(legal.TermsOfService, time.Now())

	rec := f.do(http.MethodPost, "/legal/consents", map[string]any{
		"language":  "de",
		"documents": []map[string]string{{"document": "terms-of-service", "version": inVigore.Version}},
	}, withCookie(f.token))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /legal/consents = %d: %s", rec.Code, rec.Body)
	}
	prima := statoConsensi(t, rec.Body).Accepted
	if len(prima) != 1 {
		t.Fatalf("%d consensi, atteso 1", len(prima))
	}

	rec = f.do(http.MethodGet, "/legal/consents?language=fr", nil, withCookie(f.token))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /legal/consents = %d: %s", rec.Code, rec.Body)
	}
	dopo := statoConsensi(t, rec.Body)
	if len(dopo.Accepted) != 1 || dopo.Accepted[0].Language != prima[0].Language {
		t.Errorf("la lingua del consenso è cambiata da %q a %+v chiedendo la risposta in francese",
			prima[0].Language, dopo.Accepted)
	}
	// E i documenti ancora da accettare sì: quelli si mostreranno, e il client
	// deve sapere in che lingua.
	for _, r := range dopo.Outstanding {
		if r.Language == "" {
			t.Errorf("%s: nessuna lingua dichiarata per il testo da mostrare", r.Document)
		}
	}
}

func TestUnaLinguaNonSupportataVieneRifiutata(t *testing.T) {
	f := newLegalFixture(t)

	rec := f.do(http.MethodGet, "/legal/consents?language=pt", nil, withCookie(f.token))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, atteso 400: %s", rec.Code, rec.Body)
	}
	if code := errorCode(t, rec); code != "validation_failed" {
		t.Errorf("codice = %q, atteso validation_failed", code)
	}
}

// TestLaRegistrazioneAccettaLaLinguaDellUtente verifica che il campo arrivi al
// servizio: la prova del consenso nasce lì, non su queste rotte.
func TestLaRegistrazioneAccettaLaLinguaDellUtente(t *testing.T) {
	f := newLegalFixture(t)

	rec := f.do(http.MethodPost, "/auth/register", map[string]any{
		"email": "nuova@example.com", "password": testPassword, "language": "it-IT",
	})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("registrazione con lingua valida = %d: %s", rec.Code, rec.Body)
	}

	rec = f.do(http.MethodPost, "/auth/register", map[string]any{
		"email": "altra@example.com", "password": testPassword, "language": "klingon",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("registrazione con lingua inventata = %d, atteso 400: %s", rec.Code, rec.Body)
	}
	if code := errorCode(t, rec); code != "validation_failed" {
		t.Errorf("codice = %q, atteso validation_failed", code)
	}
}
