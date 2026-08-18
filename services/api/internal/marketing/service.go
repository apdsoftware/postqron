package marketing

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"

	"github.com/apdsoftware/postqron/services/api/internal/emailrender"
)

// UnsubscribePath è il percorso della pagina di disiscrizione sull'API.
//
// Sta sull'API e non sul sito statico, e la ragione è la promessa: §2.8 dice che
// il link funziona «with one click and without signing in». Servito da qui,
// funziona senza JavaScript, senza cookie, senza CORS e senza che il frontend
// sia in piedi — cioè con il minor numero possibile di cose che possano
// impedirgli di funzionare. Una pagina statica che debba avviarsi, leggere un
// parametro e fare una richiesta a un'altra origin aggiunge tre modi di
// mancare una promessa, non uno di mantenerla.
const UnsubscribePath = "/marketing/unsubscribe"

// EnvAPIBaseURL è la radice pubblica dell'API, quella a cui i client arrivano
// davvero.
//
// Non è in internal/config per la stessa ragione delle variabili di
// internal/mailronix: il posto giusto, a regime, è lì accanto alle POSTGRES_*,
// e finché non ci arriva sta accanto a chi la usa. Serve **solo** a comporre il
// link di disiscrizione, ed è l'unico indirizzo del prodotto che
// [emailrender.Site] non conosce — quello sa il sito pubblico e la dashboard,
// non l'API.
const EnvAPIBaseURL = "POSTQRON_API_BASE_URL"

// DefaultAPIBaseURL è la produzione. È il predefinito per la stessa ragione di
// [mailronix.DefaultSite]: un link di sviluppo che punta alla produzione porta a
// una pagina che esiste, uno vuoto non porta da nessuna parte.
const DefaultAPIBaseURL = "https://api.postqron.com"

// Options configura il [Service]. Store e Signer sono obbligatori.
type Options struct {
	Store  Store
	Signer Signer

	// APIBaseURL è la radice pubblica dell'API, senza barra finale. Vuota usa
	// [DefaultAPIBaseURL].
	APIBaseURL string

	Logger *slog.Logger
}

// Service è il consenso al marketing: si presta, si ritira, si dimostra.
//
// Espone due strade per la revoca, e la differenza fra loro è tutta la
// questione di §2.8:
//
//   - [Service.Withdraw] è la revoca di chi ha una sessione, dalle
//     impostazioni dell'account;
//   - [Service.Unsubscribe] è quella del link nell'email, che funziona senza
//     accedere e la cui unica credenziale è la firma del token.
//
// Non esiste una terza strada che conceda il consenso senza sessione: vedi
// [Record.Validate] e il vincolo omonimo della migrazione 0019.
type Service struct {
	store  Store
	signer Signer
	base   string
	log    *slog.Logger
}

// NewService costruisce il servizio.
func NewService(opts Options) (*Service, error) {
	if opts.Store == nil {
		return nil, errors.New("marketing: Store è obbligatorio")
	}
	if !opts.Signer.Valid() {
		return nil, errors.New(
			"marketing: Signer è obbligatorio: senza, le email di marketing non potrebbero portare " +
				"un link di disiscrizione, e la privacy policy §2.8 promette che lo portino tutte")
	}

	base := strings.TrimSuffix(strings.TrimSpace(opts.APIBaseURL), "/")
	if base == "" {
		base = DefaultAPIBaseURL
	}
	parsed, err := url.Parse(base)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return nil, fmt.Errorf(
			"marketing: %s deve essere un indirizzo https assoluto, non %q: "+
				"è la radice del link di disiscrizione, e in un'email un indirizzo relativo "+
				"non porta da nessuna parte", EnvAPIBaseURL, base)
	}

	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{store: opts.Store, signer: opts.Signer, base: base, log: logger}, nil
}

// ------------------------------------------------------------------ consenso

// Grant registra il consenso dell'Art. 6(1)(a).
//
// Chi la chiama deve aver già verificato la sessione: la provenienza è
// [SourceProfile], e il database rifiuta un consenso che dichiari di venire dal
// link di disiscrizione.
func (s *Service) Grant(ctx context.Context, userID, ip string) (Applied, error) {
	return s.record(ctx, Record{
		UserID: userID, Decision: DecisionGranted, Source: SourceProfile, IP: ip,
	})
}

// Withdraw registra la revoca chiesta dalle impostazioni dell'account.
func (s *Service) Withdraw(ctx context.Context, userID, ip string) (Applied, error) {
	return s.record(ctx, Record{
		UserID: userID, Decision: DecisionWithdrawn, Source: SourceProfile, IP: ip,
	})
}

// Status è l'ultima decisione dell'utente.
func (s *Service) Status(ctx context.Context, userID string) (State, error) {
	if strings.TrimSpace(userID) == "" {
		return State{}, errors.New("marketing: stato senza utente")
	}
	return s.store.State(ctx, userID)
}

// History è la traccia completa: la prova che §2.8 promette di conservare.
func (s *Service) History(ctx context.Context, userID string) ([]Entry, error) {
	if strings.TrimSpace(userID) == "" {
		return nil, errors.New("marketing: traccia senza utente")
	}
	return s.store.History(ctx, userID)
}

// -------------------------------------------------------------- il link

// UnsubscribeURL compone il link che finisce in ogni email di marketing.
func (s *Service) UnsubscribeURL(userID string) string {
	return s.base + UnsubscribePath + "?token=" + url.QueryEscape(s.signer.Token(userID))
}

// Preview verifica un token **senza cambiare niente**.
//
// È ciò che risponde alla `GET` della pagina di disiscrizione, e il fatto che
// non scriva è il punto: quell'indirizzo lo visitano i crawler, i prefetch del
// browser e gli scanner antivirus dei server di posta aziendali, che aprono ogni
// link di ogni messaggio in arrivo. Se il `GET` disiscrivesse, quegli utenti
// smetterebbero di ricevere le comunicazioni **senza aver mai cliccato**, e non
// lo saprebbero: il danno è silenzioso, ed è la ragione per cui la revoca sta
// sulla `POST`.
func (s *Service) Preview(ctx context.Context, token string) (Preview, error) {
	userID, err := s.signer.Verify(token)
	if err != nil {
		return Preview{}, err
	}

	language, err := s.store.Language(ctx, userID)
	switch {
	case errors.Is(err, ErrNoRecipient):
		// L'account non c'è più. Il token era valido, ma non c'è nessuno da
		// disiscrivere e non c'è una lingua in cui parlargli: si risponde come a
		// un token che non si verifica, che è anche il modo di non confermare a
		// chi ha il link che quell'account esisteva.
		return Preview{}, ErrInvalidToken
	case err != nil:
		return Preview{}, err
	}

	state, err := s.store.State(ctx, userID)
	if err != nil {
		return Preview{}, err
	}

	return Preview{
		UserID:    userID,
		Language:  emailrender.NormalizeLanguage(language),
		Consented: state.Consented,
	}, nil
}

// Preview è ciò che la pagina di disiscrizione sa prima di agire.
//
// **Non contiene l'indirizzo email**, e non è una dimenticanza: la pagina la
// apre chiunque abbia il link, e mostrare l'indirizzo trasformerebbe un
// meccanismo di revoca in un modo di confermare a chi possiede un link a chi
// appartiene. Per disiscrivere non serve saperlo.
type Preview struct {
	UserID string
	// Language è la lingua del profilo (R33): la pagina parla la lingua
	// dell'utente, non quella del browser di chi ha aperto il link.
	Language string
	// Consented dice se c'è ancora qualcosa da fermare.
	Consented bool
}

// Unsubscribe esegue la revoca chiesta dal link, senza sessione.
//
// È idempotente: chiamarla su chi si è già disiscritto restituisce [Unchanged]
// e non allunga la traccia. Un secondo clic, un form rinviato e un `POST`
// ripetuto dal browser sono la stessa decisione, non tre.
func (s *Service) Unsubscribe(ctx context.Context, token, ip string) (Outcome, error) {
	userID, err := s.signer.Verify(token)
	if err != nil {
		return Outcome{}, err
	}

	language, err := s.store.Language(ctx, userID)
	switch {
	case errors.Is(err, ErrNoRecipient):
		return Outcome{}, ErrInvalidToken
	case err != nil:
		return Outcome{}, err
	}

	applied, err := s.record(ctx, Record{
		UserID: userID, Decision: DecisionWithdrawn, Source: SourceUnsubscribeLink, IP: ip,
	})
	if err != nil {
		return Outcome{}, err
	}
	return Outcome{
		UserID:   userID,
		Language: emailrender.NormalizeLanguage(language),
		Applied:  applied,
	}, nil
}

// Outcome è l'esito di una disiscrizione dal link.
type Outcome struct {
	UserID   string
	Language string
	Applied  Applied
}

// ------------------------------------------------------------------ interno

// record scrive una decisione e la registra nel log.
//
// Il log non nomina mai il destinatario: l'identificativo dell'utente basta a
// ritrovarlo e non è un dato personale sparso in un file di testo. Nemmeno
// l'indirizzo IP compare — sta nella traccia, dove ha uno scopo, e non nei log,
// dove non ne avrebbe.
func (s *Service) record(ctx context.Context, r Record) (Applied, error) {
	if err := r.Validate(); err != nil {
		return "", err
	}

	applied, err := s.store.Record(ctx, r)
	if err != nil {
		return "", fmt.Errorf("marketing: registrazione della decisione %s: %w", r.Decision, err)
	}

	// A livello `info` e non `debug`: è la traccia di un consenso, ed è la prima
	// cosa che si va a cercare quando qualcuno chiede conto di un'email
	// ricevuta.
	s.log.InfoContext(ctx, "decisione sul consenso al marketing registrata",
		slog.String("user_id", r.UserID),
		slog.String("decision", string(r.Decision)),
		slog.String("source", string(r.Source)),
		slog.String("applied", string(applied)))
	return applied, nil
}
