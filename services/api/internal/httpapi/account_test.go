package httpapi_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/account"
	"github.com/apdsoftware/postqron/services/api/internal/auth"
	"github.com/apdsoftware/postqron/services/api/internal/config"
	"github.com/apdsoftware/postqron/services/api/internal/httpapi"
)

// ---------------------------------------------------------------- impalcatura

// storeInMemoria è quel poco di store che serve alle rotte. Le query hanno i
// loro test contro PostgreSQL vero in internal/accountpg: qui si verifica il
// contratto HTTP — quali codici escono, cosa contiene il corpo, e quali
// credenziali bastano.
type storeInMemoria struct {
	status    account.Status
	requested int
	canceled  int
}

func (s *storeInMemoria) Status(context.Context, string) (account.Status, error) {
	return s.status, nil
}

func (s *storeInMemoria) RequestDeletion(_ context.Context, _ string, at, purgeAfter time.Time) (account.Receipt, error) {
	if s.status.Requested {
		return account.Receipt{}, account.ErrAlreadyRequested
	}
	s.requested++
	s.status.Requested = true
	s.status.RequestedAt, s.status.PurgeAfter = at, purgeAfter
	return account.Receipt{
		RequestedAt: at, PurgeAfter: purgeAfter,
		JobsStopped: 2, KeysRevoked: 1, SecretsRevoked: 3, AIKeysRevoked: 1, SessionsRevoked: 4,
	}, nil
}

func (s *storeInMemoria) CancelDeletion(context.Context, string) (account.Restored, error) {
	if !s.status.Requested {
		return account.Restored{}, account.ErrNotRequested
	}
	s.canceled++
	s.status = account.Status{Subscription: s.status.Subscription}
	return account.Restored{JobsResumed: 2}, nil
}

func (s *storeInMemoria) DueForPurge(context.Context, time.Time, int) ([]string, error) {
	return nil, nil
}

func (s *storeInMemoria) Purge(context.Context, string) (account.Purged, error) {
	return account.Purged{}, nil
}

type accountFixture struct {
	*api
	store *storeInMemoria
	user  auth.User
	token string
}

func newAccountFixture(t *testing.T, tune ...func(*storeInMemoria)) *accountFixture {
	t.Helper()

	store := &storeInMemoria{}
	for _, fn := range tune {
		fn(store)
	}

	// Il confermatore è **il servizio di autenticazione vero**, ed è ciò che rende
	// questo test una verifica della conferma di R45 e non di un doppione che dice
	// sempre di sì. L'indirezione serve solo all'ordine di costruzione: `newAPI`
	// costruisce l'autenticazione *dopo* aver chiamato questa funzione, quindi il
	// riferimento si riempie appena esiste.
	conferma := &confermaTardiva{}
	a := newAPI(t, func(_ *config.Config, _ *auth.Options, deps *httpapi.Deps) {
		svc, err := account.New(account.Options{
			Store:   store,
			Confirm: conferma,
			Grace:   48 * time.Hour,
			Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		})
		if err != nil {
			t.Fatalf("account.New: %v", err)
		}
		deps.Account = svc
	})
	conferma.svc = a.svc

	user, token := a.registerAndLogin()
	return &accountFixture{api: a, store: store, user: user, token: token}
}

// confermaTardiva inoltra la conferma al servizio di autenticazione, che esiste
// solo dopo la costruzione del router.
type confermaTardiva struct{ svc *auth.Service }

func (c *confermaTardiva) ConfirmPassword(ctx context.Context, userID, password string) error {
	return c.svc.ConfirmPassword(ctx, userID, password)
}

// ------------------------------------------------------------------- rotte

// TestLaCancellazioneEsigeLaPasswordAncheConUnaSessioneValida è la conferma di
// R45 vista dall'API: la sessione basta a *chiedere lo stato*, non a cancellare.
func TestLaCancellazioneEsigeLaPasswordAncheConUnaSessioneValida(t *testing.T) {
	f := newAccountFixture(t)

	casi := map[string]struct {
		corpo  map[string]any
		status int
		codice string
	}{
		"senza password": {
			corpo: map[string]any{}, status: http.StatusBadRequest, codice: "validation_failed",
		},
		"password sbagliata": {
			corpo:  map[string]any{"password": "non-e-quella"},
			status: http.StatusUnauthorized, codice: "invalid_credentials",
		},
	}
	for nome, caso := range casi {
		t.Run(nome, func(t *testing.T) {
			rec := f.do(http.MethodPost, "/account/deletion", caso.corpo, withCookie(f.token))
			if rec.Code != caso.status {
				t.Fatalf("status = %d, atteso %d: %s", rec.Code, caso.status, rec.Body)
			}
			if code := errorCode(t, rec); code != caso.codice {
				t.Errorf("codice = %q, atteso %q", code, caso.codice)
			}
		})
	}
	if f.store.requested != 0 {
		t.Error("una richiesta è arrivata allo store senza una password corretta")
	}
}

// TestUnaChiaveAPINonPuòCancellareLAccount: una credenziale di servizio,
// magari dimenticata in un file di configurazione, non deve poter distruggere il
// lavoro di qualcuno.
func TestUnaChiaveAPINonPuòCancellareLAccount(t *testing.T) {
	f := newAccountFixture(t)

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

	for _, percorso := range []struct{ metodo, path string }{
		{http.MethodGet, "/account/deletion"},
		{http.MethodPost, "/account/deletion"},
		{http.MethodDelete, "/account/deletion"},
	} {
		rec := f.do(percorso.metodo, percorso.path,
			map[string]any{"password": testPassword}, withKey(creata.Secret))
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s con una chiave API = %d, atteso 401: %s",
				percorso.metodo, percorso.path, rec.Code, rec.Body)
		}
	}
	if f.store.requested != 0 {
		t.Error("una chiave API è riuscita a chiedere la cancellazione dell'account")
	}
}

// TestLaRichiestaRispondeConCiòCheHaFermato verifica il corpo della risposta.
//
// I numeri ci sono perché la privacy policy §5 promette che le esecuzioni si
// fermano e le chiavi si revocano *immediatamente*: una risposta che dicesse
// solo «va bene» lascerebbe all'utente il compito di verificarlo altrove.
func TestLaRichiestaRispondeConCiòCheHaFermato(t *testing.T) {
	f := newAccountFixture(t)

	rec := f.do(http.MethodPost, "/account/deletion",
		map[string]any{"password": testPassword}, withCookie(f.token))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, atteso 202: %s", rec.Code, rec.Body)
	}

	var body httpapi.DeletionReceiptResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decodifica: %v", err)
	}
	switch {
	case body.JobsStopped != 2:
		t.Errorf("job fermati = %d, attesi 2", body.JobsStopped)
	case body.KeysRevoked != 1 || body.SecretsRevoked != 3 || body.AIKeysRevoked != 1:
		t.Errorf("revoche non riportate: %+v", body)
	case body.SessionsRevoked != 4:
		t.Errorf("sessioni chiuse = %d, attese 4", body.SessionsRevoked)
	case !body.PurgeAfter.After(body.RequestedAt):
		t.Errorf("la scadenza (%v) non segue la richiesta (%v)", body.PurgeAfter, body.RequestedAt)
	}

	// Ripetuta, la richiesta non sposta la scadenza.
	rec = f.do(http.MethodPost, "/account/deletion",
		map[string]any{"password": testPassword}, withCookie(f.token))
	if rec.Code != http.StatusConflict {
		t.Fatalf("seconda richiesta = %d, atteso 409: %s", rec.Code, rec.Body)
	}
	if code := errorCode(t, rec); code != "deletion_already_requested" {
		t.Errorf("codice = %q, atteso deletion_already_requested", code)
	}
}

// TestLAbbonamentoVivoRifiutaLaPrimaRichiestaEPassaLaSeconda è il contratto HTTP
// della presa d'atto su Paddle.
//
// Il 409 porta il piano nel corpo perché la dashboard deve poter dire *cosa*
// resterà vivo: senza, dovrebbe fare una seconda richiesta per costruire il
// messaggio, e nel frattempo mostrerebbe un errore che non spiega niente.
func TestLAbbonamentoVivoRifiutaLaPrimaRichiestaEPassaLaSeconda(t *testing.T) {
	f := newAccountFixture(t, func(s *storeInMemoria) {
		s.status.Subscription = account.Subscription{
			Paid: true, PlanCode: "pro", PaddleSubscriptionID: "sub_01",
		}
	})

	rec := f.do(http.MethodPost, "/account/deletion",
		map[string]any{"password": testPassword}, withCookie(f.token))
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, atteso 409: %s", rec.Code, rec.Body)
	}
	var errBody httpapi.ErrorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &errBody); err != nil {
		t.Fatalf("decodifica: %v", err)
	}
	if errBody.Error.Code != "subscription_active" {
		t.Fatalf("codice = %q, atteso subscription_active", errBody.Error.Code)
	}
	if errBody.Error.Plan != "pro" {
		t.Errorf("piano nel corpo = %q, atteso pro", errBody.Error.Plan)
	}

	// «You may close your account at any time» (Termini §7): con la presa d'atto
	// si procede.
	rec = f.do(http.MethodPost, "/account/deletion",
		map[string]any{"password": testPassword, "subscription_acknowledged": true},
		withCookie(f.token))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("richiesta con presa d'atto = %d, atteso 202: %s", rec.Code, rec.Body)
	}
}

// TestLoStatoAnticipaLAbbonamentoPrimaCheLUtenteProvi: il rifiuto non deve
// essere la prima volta che l'utente scopre di avere un abbonamento vivo.
func TestLoStatoAnticipaLAbbonamentoPrimaCheLUtenteProvi(t *testing.T) {
	f := newAccountFixture(t, func(s *storeInMemoria) {
		s.status.Subscription = account.Subscription{
			Paid: true, PlanCode: "team", PaddleSubscriptionID: "sub_42",
		}
	})

	rec := f.do(http.MethodGet, "/account/deletion", nil, withCookie(f.token))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, atteso 200: %s", rec.Code, rec.Body)
	}
	var body httpapi.DeletionStatusResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decodifica: %v", err)
	}
	switch {
	case body.Requested:
		t.Error("nessuna cancellazione è stata chiesta, e lo stato dice il contrario")
	case body.Subscription == nil:
		t.Fatal("lo stato non nomina l'abbonamento vivo")
	case body.Subscription.PlanCode != "team" || body.Subscription.PaddleSubscriptionID != "sub_42":
		t.Errorf("abbonamento = %+v", body.Subscription)
	case body.GraceHours != 48:
		t.Errorf("grazia = %v ore, attese 48: la schermata di conferma deve poterla dire", body.GraceHours)
	}
}

// TestLAnnullamentoChiudeLaFinestra copre il «during which you can change your
// mind» della privacy policy §5, e il caso in cui non c'è niente da annullare.
func TestLAnnullamentoChiudeLaFinestra(t *testing.T) {
	f := newAccountFixture(t)

	rec := f.do(http.MethodDelete, "/account/deletion", nil, withCookie(f.token))
	if rec.Code != http.StatusConflict {
		t.Fatalf("annullamento senza richiesta = %d, atteso 409: %s", rec.Code, rec.Body)
	}
	if code := errorCode(t, rec); code != "deletion_not_requested" {
		t.Errorf("codice = %q, atteso deletion_not_requested", code)
	}

	if rec := f.do(http.MethodPost, "/account/deletion",
		map[string]any{"password": testPassword}, withCookie(f.token)); rec.Code != http.StatusAccepted {
		t.Fatalf("richiesta = %d: %s", rec.Code, rec.Body)
	}

	rec = f.do(http.MethodDelete, "/account/deletion", nil, withCookie(f.token))
	if rec.Code != http.StatusOK {
		t.Fatalf("annullamento = %d, atteso 200: %s", rec.Code, rec.Body)
	}
	var body httpapi.DeletionRestoredResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decodifica: %v", err)
	}
	if body.JobsResumed != 2 {
		t.Errorf("job riaccesi = %d, attesi 2", body.JobsResumed)
	}

	// Lo stato torna a dire che non c'è nessuna cancellazione in corso.
	rec = f.do(http.MethodGet, "/account/deletion", nil, withCookie(f.token))
	var stato httpapi.DeletionStatusResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &stato); err != nil {
		t.Fatalf("decodifica: %v", err)
	}
	if stato.Requested {
		t.Error("dopo l'annullamento lo stato dichiara ancora una cancellazione in corso")
	}
}

// TestSenzaSessioneLeRotteDiCancellazioneRispondono401: nessuna di loro deve
// essere raggiungibile da chi non si è autenticato.
func TestSenzaSessioneLeRotteDiCancellazioneRispondono401(t *testing.T) {
	f := newAccountFixture(t)

	for _, caso := range []struct{ metodo, path string }{
		{http.MethodGet, "/account/deletion"},
		{http.MethodPost, "/account/deletion"},
		{http.MethodDelete, "/account/deletion"},
	} {
		rec := f.do(caso.metodo, caso.path, map[string]any{"password": testPassword})
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s senza sessione = %d, atteso 401", caso.metodo, caso.path, rec.Code)
		}
	}
}

// TestSenzaIlServizioLeRotteNonEsistono: la degradazione dichiarata in
// [httpapi.Deps] è che le rotte non vengano registrate affatto.
func TestSenzaIlServizioLeRotteNonEsistono(t *testing.T) {
	a := newAPI(t)
	_, token := a.registerAndLogin()

	rec := a.do(http.MethodGet, "/account/deletion", nil, withCookie(token))
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, atteso 404", rec.Code)
	}
}

// TestLaPasswordNonFinisceNeiLog è la stessa regola di `/secrets`: il corpo di
// questa richiesta porta una password, e il messaggio del decodificatore JSON
// cita il testo che non è riuscito a leggere.
func TestLaPasswordNonFinisceNeiLog(t *testing.T) {
	f := newAccountFixture(t)

	rec := f.do(http.MethodPost, "/account/deletion", map[string]any{
		"password":             testPassword,
		"campo-che-non-esiste": 1,
	}, withCookie(f.token))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, atteso 400: %s", rec.Code, rec.Body)
	}
	if body := rec.Body.String(); strings.Contains(body, testPassword) {
		t.Errorf("la password compare nella risposta: %s", body)
	}
	if logs := f.logs.String(); strings.Contains(logs, testPassword) {
		t.Errorf("la password compare nei log: %s", logs)
	}
}
