package legal_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/legal"
)

// storeFinto è uno [legal.Store] in memoria con l'unica proprietà che conta:
// **l'idempotenza sulla coppia (documento, versione)**. Un secondo consenso
// sulla stessa versione non crea una riga e non tocca la data della prima, come
// fa il vincolo unico della 0018.
type storeFinto struct {
	righe map[string][]legal.Consent
}

func nuovoStore() *storeFinto { return &storeFinto{righe: map[string][]legal.Consent{}} }

func (s *storeFinto) ConsentsOf(_ context.Context, userID string) ([]legal.Consent, error) {
	return append([]legal.Consent(nil), s.righe[userID]...), nil
}

func (s *storeFinto) Record(_ context.Context, userID string, consents []legal.Consent) error {
	for _, c := range consents {
		if s.presente(userID, c) {
			continue
		}
		s.righe[userID] = append(s.righe[userID], c)
	}
	return nil
}

func (s *storeFinto) presente(userID string, c legal.Consent) bool {
	for _, esistente := range s.righe[userID] {
		if esistente.Document == c.Document && esistente.Version == c.Version {
			return true
		}
	}
	return false
}

// ------------------------------------------------------------------ fixture

// due documenti a versioni diverse: è la situazione vera di `legal/`, ridotta al
// minimo che serve a provarla.
var (
	quandoNasce = time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)
	termini100  = legal.Release{
		Version: "1.0.0", Effective: legal.Date(2026, 8, 1), Announced: legal.Date(2026, 8, 1),
		Notice: legal.NoticeFirstPublication,
		Texts:  map[legal.Language]legal.Text{legal.English: {SHA256: impronta}},
	}
	termini200 = legal.Release{
		Version: "2.0.0", Effective: legal.Date(2026, 10, 1), Announced: legal.Date(2026, 8, 15),
		Notice: legal.NoticeMaterial,
		Texts: map[legal.Language]legal.Text{
			legal.English: {File: "terms-of-service.2.0.0.md", SHA256: impronta},
			legal.Italian: {File: "terms-of-service.2.0.0.md", SHA256: impronta},
		},
	}
	privacy110 = legal.Release{
		Version: "1.1.0", Effective: legal.Date(2026, 8, 10), Announced: legal.Date(2026, 8, 10),
		Notice: legal.NoticeFirstPublication,
		Texts:  map[legal.Language]legal.Text{legal.English: {SHA256: impronta}},
	}
)

func servizio(t *testing.T, store legal.Store, adesso time.Time) *legal.Service {
	t.Helper()
	reg, err := legal.NewRegistry(map[legal.Document][]legal.Release{
		legal.TermsOfService:      {termini100, termini200},
		legal.PrivacyPolicy:       {privacy110},
		legal.AcceptableUsePolicy: {rilascio("1.0.0", legal.Date(2026, 8, 1), legal.NoticeFirstPublication)},
		legal.CookiePolicy:        {rilascio("1.0.0", legal.Date(2026, 8, 1), legal.NoticeFirstPublication)},
	})
	if err != nil {
		t.Fatalf("registro di prova: %v", err)
	}
	svc, err := legal.New(legal.Options{
		Store:    store,
		Registry: reg,
		Now:      func() time.Time { return adesso },
	})
	if err != nil {
		t.Fatalf("servizio: %v", err)
	}
	return svc
}

// ------------------------------------------------------------------- test

// TestDueDocumentiAVersioniDiverseNonSiConfondono è la prova del fatto che ha
// deciso la forma della tabella: una versione sola per l'insieme dei documenti
// sarebbe già falsa oggi.
func TestDueDocumentiAVersioniDiverseNonSiConfondono(t *testing.T) {
	store := nuovoStore()
	svc := servizio(t, store, quandoNasce)

	consensi := svc.Registry().ConsentsFor(quandoNasce, legal.Italian, legal.SourceRegistration)
	if err := store.Record(t.Context(), "utente", consensi); err != nil {
		t.Fatalf("registrazione: %v", err)
	}

	stato, err := svc.State(t.Context(), "utente", legal.Italian)
	if err != nil {
		t.Fatalf("stato: %v", err)
	}
	if len(stato.Outstanding) != 0 {
		t.Errorf("dopo aver accettato tutto restano %d documenti da accettare: %v",
			len(stato.Outstanding), stato.Outstanding)
	}

	versioni := map[legal.Document]string{}
	for _, c := range stato.Accepted {
		versioni[c.Document] = c.Version
	}
	if versioni[legal.TermsOfService] != "1.0.0" {
		t.Errorf("Termini accettati alla %q, attesa la 1.0.0 (la 2.0.0 è annunciata, non in vigore)",
			versioni[legal.TermsOfService])
	}
	if versioni[legal.PrivacyPolicy] != "1.1.0" {
		t.Errorf("privacy policy accettata alla %q, attesa la 1.1.0", versioni[legal.PrivacyPolicy])
	}
}

// TestLaLinguaRegistrataÈQuellaDelTestoMostrato copre la parte di R46 che si
// dimentica: il consenso vale su ciò che l'utente ha **letto**.
func TestLaLinguaRegistrataÈQuellaDelTestoMostrato(t *testing.T) {
	svc := servizio(t, nuovoStore(), quandoNasce)

	for _, c := range svc.Registry().ConsentsFor(quandoNasce, legal.Italian, legal.SourceRegistration) {
		// Nessuno dei documenti in vigore oggi ha la traduzione italiana: il
		// testo mostrato è l'inglese, e la prova deve dirlo. Registrare «it»
		// sarebbe affermare che l'utente ha letto un testo che non esiste.
		if c.Language != legal.English {
			t.Errorf("%s: registrata la lingua %q su un documento che esiste solo in inglese",
				c.Document, c.Language)
		}
	}
}

// TestUnDocumentoCheCambiaTornaFraQuelliDaAccettare è la storia dei Termini §9
// vista dal codice: il preavviso, l'entrata in vigore, e il consenso vecchio che
// resta dov'è.
func TestUnDocumentoCheCambiaTornaFraQuelliDaAccettare(t *testing.T) {
	store := nuovoStore()

	// Giorno 1: l'utente accetta tutto quello che vige.
	svc := servizio(t, store, quandoNasce)
	if err := store.Record(t.Context(), "utente",
		svc.Registry().ConsentsFor(quandoNasce, legal.English, legal.SourceRegistration)); err != nil {
		t.Fatalf("registrazione: %v", err)
	}

	// Durante il preavviso: niente da accettare, e il cambiamento è annunciato.
	durante := servizio(t, store, time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC))
	stato, err := durante.State(t.Context(), "utente", legal.English)
	if err != nil {
		t.Fatalf("stato durante il preavviso: %v", err)
	}
	if len(stato.Outstanding) != 0 {
		t.Errorf("durante il preavviso l'utente dovrebbe non dover accettare niente, e ha %d documenti in sospeso",
			len(stato.Outstanding))
	}
	if len(stato.Upcoming) != 1 || stato.Upcoming[0].Version != "2.0.0" || !stato.Upcoming[0].Material {
		t.Fatalf("cambiamenti annunciati = %+v, atteso il solo passaggio materiale alla 2.0.0", stato.Upcoming)
	}
	if preavviso := stato.Upcoming[0].Effective.Sub(stato.Upcoming[0].Announced); preavviso < legal.MaterialChangeNotice {
		t.Errorf("preavviso annunciato di %v, i Termini §9 ne promettono %v", preavviso, legal.MaterialChangeNotice)
	}

	// Entrata in vigore: i Termini tornano fra i documenti da accettare, e la
	// riga dice **quale** versione l'utente aveva accettato prima.
	dopo := servizio(t, store, time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC))
	stato, err = dopo.State(t.Context(), "utente", legal.English)
	if err != nil {
		t.Fatalf("stato dopo l'entrata in vigore: %v", err)
	}
	if len(stato.Outstanding) != 1 {
		t.Fatalf("documenti da accettare = %+v, atteso il solo terms-of-service", stato.Outstanding)
	}
	mancante := stato.Outstanding[0]
	if mancante.Document != legal.TermsOfService || mancante.Version != "2.0.0" {
		t.Errorf("da accettare %s %s, atteso terms-of-service 2.0.0", mancante.Document, mancante.Version)
	}
	if mancante.AcceptedVersion != "1.0.0" {
		t.Errorf("versione accettata in precedenza = %q, attesa 1.0.0: senza, il client non può distinguere "+
			"«non hai mai accettato» da «è cambiato»", mancante.AcceptedVersion)
	}

	// E l'utente accetta la versione nuova: quella vecchia **resta**, perché è
	// la prova di cosa vincolava allora.
	stato, err = dopo.Accept(t.Context(), "utente", legal.AcceptInput{
		Language: legal.Italian,
		Accept:   []legal.Acceptance{{Document: legal.TermsOfService, Version: "2.0.0"}},
	})
	if err != nil {
		t.Fatalf("accettazione: %v", err)
	}
	if len(stato.Outstanding) != 0 {
		t.Errorf("dopo l'accettazione restano %d documenti in sospeso", len(stato.Outstanding))
	}

	var versioniTermini []string
	var lingue []legal.Language
	for _, c := range stato.Accepted {
		if c.Document == legal.TermsOfService {
			versioniTermini = append(versioniTermini, c.Version)
			lingue = append(lingue, c.Language)
		}
	}
	if len(versioniTermini) != 2 || versioniTermini[0] != "1.0.0" || versioniTermini[1] != "2.0.0" {
		t.Errorf("versioni dei Termini accettate = %v, attese entrambe: la vecchia è la prova di cosa vincolava allora",
			versioniTermini)
	}
	// La 2.0.0 esiste anche in italiano: la seconda accettazione registra «it»,
	// e la prima resta «en». Le due lingue diverse sulla stessa storia sono
	// esattamente ciò che R46 chiede di poter distinguere.
	if len(lingue) == 2 && (lingue[0] != legal.English || lingue[1] != legal.Italian) {
		t.Errorf("lingue registrate = %v, attese [en it]", lingue)
	}
}

// TestNonSiPuòAccettareUnaVersioneCheNonÈInVigore copre la corsa fra la pagina
// che si carica e il bottone che si preme.
func TestNonSiPuòAccettareUnaVersioneCheNonÈInVigore(t *testing.T) {
	svc := servizio(t, nuovoStore(), quandoNasce)

	casi := map[string]string{
		"una versione futura":    "2.0.0",
		"una versione inventata": "9.9.9",
	}
	for nome, versione := range casi {
		_, err := svc.Accept(t.Context(), "utente", legal.AcceptInput{
			Language: legal.English,
			Accept:   []legal.Acceptance{{Document: legal.TermsOfService, Version: versione}},
		})
		if !errors.Is(err, legal.ErrVersionNotInForce) {
			t.Errorf("%s: errore = %v, atteso ErrVersionNotInForce", nome, err)
			continue
		}
		var dettaglio *legal.VersionNotInForceError
		if !errors.As(err, &dettaglio) || dettaglio.InForce != "1.0.0" || dettaglio.Offered != versione {
			t.Errorf("%s: l'errore non dice quale versione è stata offerta e quale vincola: %v", nome, err)
		}
	}
}

// TestAccettareDueVolteNonSpostaLaData è la proprietà che rende la traccia una
// prova: la data di un consenso è l'istante in cui l'utente si è vincolato, e un
// doppio invio del form non deve poterla spostare.
func TestAccettareDueVolteNonSpostaLaData(t *testing.T) {
	store := nuovoStore()
	primo := servizio(t, store, quandoNasce)

	if _, err := primo.Accept(t.Context(), "utente", legal.AcceptInput{
		Language: legal.English,
		Accept:   []legal.Acceptance{{Document: legal.TermsOfService, Version: "1.0.0"}},
	}); err != nil {
		t.Fatalf("prima accettazione: %v", err)
	}

	piùTardi := quandoNasce.Add(72 * time.Hour)
	stato, err := servizio(t, store, piùTardi).Accept(t.Context(), "utente", legal.AcceptInput{
		Language: legal.English,
		Accept:   []legal.Acceptance{{Document: legal.TermsOfService, Version: "1.0.0"}},
	})
	if err != nil {
		t.Fatalf("seconda accettazione: %v", err)
	}

	var righe []legal.Consent
	for _, c := range stato.Accepted {
		if c.Document == legal.TermsOfService {
			righe = append(righe, c)
		}
	}
	if len(righe) != 1 {
		t.Fatalf("%d righe per la stessa versione: un doppio invio del form non deve moltiplicare la prova", len(righe))
	}
	if !righe[0].AcceptedAt.Equal(quandoNasce) {
		t.Errorf("data del consenso = %s, attesa %s: la seconda accettazione l'ha spostata in avanti",
			righe[0].AcceptedAt, quandoNasce)
	}
}

func TestUnAccettazioneVuotaOAmbiguaVieneRifiutata(t *testing.T) {
	svc := servizio(t, nuovoStore(), quandoNasce)

	if _, err := svc.Accept(t.Context(), "utente", legal.AcceptInput{Language: legal.English}); !errors.Is(err, legal.ErrNoDocuments) {
		t.Errorf("accettazione vuota: errore = %v, atteso ErrNoDocuments", err)
	}

	_, err := svc.Accept(t.Context(), "utente", legal.AcceptInput{
		Language: legal.English,
		Accept: []legal.Acceptance{
			{Document: legal.TermsOfService, Version: "1.0.0"},
			{Document: legal.TermsOfService, Version: "1.0.0"},
		},
	})
	if !errors.Is(err, legal.ErrDuplicateDocument) {
		t.Errorf("documento ripetuto: errore = %v, atteso ErrDuplicateDocument", err)
	}

	_, err = svc.Accept(t.Context(), "utente", legal.AcceptInput{
		Language: "pt",
		Accept:   []legal.Acceptance{{Document: legal.TermsOfService, Version: "1.0.0"}},
	})
	if !errors.Is(err, legal.ErrUnknownLanguage) {
		t.Errorf("lingua non supportata: errore = %v, atteso ErrUnknownLanguage", err)
	}
}
