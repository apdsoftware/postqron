package apikeys

import (
	"context"
	"crypto/hmac"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"slices"
	"strings"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/auth"
	"github.com/apdsoftware/postqron/services/api/internal/ratelimit"
)

// Vincoli sulla creazione. Sono nel codice e non in configurazione per la stessa
// ragione delle durate di internal/auth: sono scelte di sicurezza e di forma
// dello schema, non parametri d'esercizio.
const (
	// maxNameLength è il limite del nome, allineato al CHECK della 0002.
	maxNameLength = 100

	// MaxActiveKeys è il numero massimo di chiavi vive per utente.
	//
	// **Non è un limite di piano.** È un tetto tecnico contro la creazione senza
	// fine di righe da parte di un account autenticato, ed è deliberatamente
	// altissimo rispetto a qualunque uso reale: chi ne ha bisogno di più di cento
	// sta facendo qualcos'altro. Il limite *commerciale* — quante chiavi include
	// ogni piano — è R15 e appartiene a #398, e quando arriverà va applicato
	// **prima** di questo, non al suo posto.
	MaxActiveKeys = 100

	// maxExpiry è la scadenza massima impostabile alla creazione. Due anni: oltre,
	// una data di scadenza non è più una misura di sicurezza ma un promemoria che
	// nessuno leggerà.
	maxExpiry = 2 * 365 * 24 * time.Hour

	// touchInterval è la granularità con cui si aggiorna `last_used_at`.
	//
	// Senza soglia, ogni richiesta autenticata con chiave sarebbe anche una
	// scrittura — e per un'API, a differenza di una dashboard, le richieste
	// autenticate sono il traffico. Con cinque minuti «ultimo utilizzo» resta
	// esatto al minuto e le scritture diventano una frazione delle letture.
	//
	// È lo stesso valore di internal/auth, e la scelta è la stessa.
	touchInterval = 5 * time.Minute
)

// Limits raccoglie le regole di rate limiting delle chiavi API.
type Limits struct {
	// AuthPerIP limita i tentativi di autenticazione **falliti** con chiave da uno
	// stesso indirizzo.
	//
	// Non è il rate limiting generale delle API — quello è R10 e sta in #398, per
	// piano, e non va costruito qui. Questo tetto serve a una cosa sola: evitare
	// che «autenticati con una chiave qualunque» diventi un modo di far lavorare
	// database e CPU a costo zero per chi chiama. Le chiavi hanno 256 bit di
	// entropia, quindi non è la forzatura che si sta fermando.
	//
	// Il credito si consuma a ogni tentativo e si azzera a ogni successo: il
	// traffico legittimo, che riesce, non accumula mai nulla.
	AuthPerIP ratelimit.Rule

	// CreatePerUser limita le creazioni di chiavi di uno stesso account. Non è una
	// difesa contro un estraneo — per arrivare qui serve una sessione — ma contro
	// uno script che sbaglia in un ciclo.
	CreatePerUser ratelimit.Rule
}

// DefaultLimits sono i limiti applicati se non se ne indicano altri.
func DefaultLimits() Limits {
	return Limits{
		AuthPerIP:     ratelimit.Rule{Burst: 30, Window: 5 * time.Minute},
		CreatePerUser: ratelimit.Rule{Burst: 10, Window: time.Hour},
	}
}

// Client descrive la provenienza di una richiesta. Serve al rate limiting; vale
// lo stesso discorso di [auth.Client] sul perché è un parametro esplicito.
type Client struct {
	IP netip.Addr
}

func (c Client) ipKey() string {
	if !c.IP.IsValid() {
		return "sconosciuto"
	}
	return c.IP.Unmap().String()
}

// Options configura il [Service]. Store, Users e Keyring sono obbligatori.
type Options struct {
	Store   Store
	Users   Users
	Keyring auth.Keyring
	Logger  *slog.Logger

	// Now sostituisce l'orologio. Serve ai test sulle scadenze.
	Now func() time.Time

	// Limits sostituisce [DefaultLimits]. Il campo zero usa i default; una
	// singola regola con Burst zero usa il default di quella regola.
	Limits Limits
}

// Service è la gestione delle chiavi API. Va costruito con [NewService].
type Service struct {
	store Store
	users Users
	keys  auth.Keyring
	log   *slog.Logger
	now   func() time.Time

	authIP     *ratelimit.Limiter
	createUser *ratelimit.Limiter
}

// NewService costruisce il servizio.
func NewService(opts Options) (*Service, error) {
	if opts.Store == nil {
		return nil, errors.New("apikeys: Store è obbligatorio")
	}
	if opts.Users == nil {
		return nil, errors.New("apikeys: Users è obbligatorio")
	}
	if !opts.Keyring.Valid() {
		return nil, errors.New("apikeys: Keyring non inizializzato")
	}

	s := &Service{
		store: opts.Store,
		users: opts.Users,
		keys:  opts.Keyring,
		log:   opts.Logger,
		now:   opts.Now,
	}
	if s.log == nil {
		s.log = slog.Default()
	}
	if s.now == nil {
		s.now = time.Now
	}

	limits := mergeLimits(opts.Limits)
	clock := ratelimit.WithClock(s.now)
	var err error
	if s.authIP, err = ratelimit.New(limits.AuthPerIP, clock); err != nil {
		return nil, fmt.Errorf("apikeys: AuthPerIP: %w", err)
	}
	if s.createUser, err = ratelimit.New(limits.CreatePerUser, clock); err != nil {
		return nil, fmt.Errorf("apikeys: CreatePerUser: %w", err)
	}
	return s, nil
}

// ------------------------------------------------------------------ creazione

// CreateInput sono i dati di una creazione.
type CreateInput struct {
	Name   string
	Scopes []Scope
	// ExpiresAt è la scadenza, facoltativa. Nil significa «non scade».
	ExpiresAt *time.Time
	Client    Client
}

// Create emette una chiave nuova.
//
// **È l'unico punto del sistema in cui la chiave in chiaro esiste.** Viene
// generata qui, il suo HMAC finisce nello [Store], e il valore in chiaro torna
// al chiamante dentro [Created]. Da quel momento il sistema non ne ha più
// nessuna copia: non c'è una colonna che la contenga, non c'è un metodo dello
// [Store] che la restituisca, e la rotta di elenco serializza [Key], che non ha
// il campo.
func (s *Service) Create(ctx context.Context, userID string, in CreateInput) (Created, error) {
	now := s.now()
	name, scopes, expires, err := validateCreate(in, now)
	if err != nil {
		return Created{}, err
	}
	if err := allow(s.createUser, userID); err != nil {
		return Created{}, err
	}

	active, err := s.store.CountActiveKeys(ctx, userID, now)
	if err != nil {
		return Created{}, fmt.Errorf("conteggio delle chiavi attive: %w", err)
	}
	if active >= MaxActiveKeys {
		return Created{}, ErrTooManyKeys
	}

	secret, prefix, err := newToken()
	if err != nil {
		return Created{}, err
	}

	key, err := s.store.CreateKey(ctx, Key{
		UserID:    userID,
		Name:      name,
		Prefix:    prefix,
		Hash:      s.keys.APIKeyHash(secret),
		Scopes:    scopes,
		ExpiresAt: expires,
		CreatedAt: now,
	})
	if err != nil {
		return Created{}, fmt.Errorf("creazione della chiave: %w", err)
	}

	// Nel log ci va la chiave secondo [Key.LogValue]: identificativo, prefisso e
	// quanti scope. Non il segreto, e nemmeno la sua impronta.
	s.log.InfoContext(ctx, "chiave API creata", slog.Any("api_key", key))
	return Created{Key: key, Secret: secret}, nil
}

// validateCreate normalizza e verifica i dati di una creazione.
//
// I motivi di rifiuto si raccolgono tutti invece di fermarsi al primo: chi
// compila un form vuole sapere tutto quello che c'è da correggere in un giro.
func validateCreate(in CreateInput, now time.Time) (string, []Scope, *time.Time, error) {
	invalid := &ValidationError{}

	name := strings.TrimSpace(in.Name)
	switch {
	case name == "":
		invalid.add("name", "required", "Dai un nome alla chiave: è l'unico modo di sapere quale revocare.")
	case len(name) > maxNameLength:
		invalid.add("name", "too_long",
			fmt.Sprintf("Il nome non può superare %d caratteri.", maxNameLength))
	}

	// Gli scope si deduplicano perché la 0002 impone `has_unique_elements` sulla
	// colonna: un doppione arriverebbe fino al database e lì diventerebbe un
	// errore di vincolo, cioè un 500 al posto di un 400.
	scopes := make([]Scope, 0, len(in.Scopes))
	for _, scope := range in.Scopes {
		switch {
		case !scope.Valid():
			invalid.add("scopes", "unknown_scope",
				fmt.Sprintf("Scope %q sconosciuto. Ammessi: %s.", scope, joinScopes(allScopes)))
		case !slices.Contains(scopes, scope):
			scopes = append(scopes, scope)
		}
	}
	if len(scopes) == 0 && len(invalid.Fields) == 0 {
		// Una chiave senza scope è ammessa dallo schema ma non serve a niente, e chi
		// la crea non lo sta facendo di proposito: quasi sempre è il campo
		// dimenticato nella richiesta. Rifiutarla adesso costa un 400; accettarla
		// costa una segnalazione «la mia chiave dà 403 su tutto».
		invalid.add("scopes", "required",
			"Indica almeno uno scope: una chiave senza scope non può fare nulla. Ammessi: "+
				joinScopes(allScopes)+".")
	}

	// La scadenza si valuta rispetto a `now` e non a `time.Now`: l'orologio del
	// servizio è sostituibile, e una scadenza «nel passato» dipende da quando la si
	// guarda.
	expires := in.ExpiresAt
	if expires != nil {
		utc := expires.UTC()
		expires = &utc
		switch {
		case !utc.After(now):
			invalid.add("expires_at", "in_the_past", "La scadenza dev'essere nel futuro.")
		case utc.Sub(now) > maxExpiry:
			invalid.add("expires_at", "too_far",
				fmt.Sprintf("La scadenza non può superare %d giorni.", int(maxExpiry.Hours()/24)))
		}
	}

	return name, scopes, expires, invalid.orNil()
}

// ------------------------------------------------------------ autenticazione

// Authenticate risolve una chiave API in chiaro nel suo proprietario.
//
// # Perché la ricerca non scandisce le chiavi
//
// L'impronta è deterministica, quindi la chiave si cerca con un'uguaglianza
// sull'indice unico `api_keys_key_hash_key`: una lettura, sempre la stessa, che
// non dipende da quante chiavi esistono. È l'alternativa che si perde adottando
// un hash con salt per riga — lì l'unico modo di trovare la chiave giusta è
// provarle tutte, e per farlo in tempo utile bisogna prima restringere il campo
// con un prefisso in chiaro, cioè introdurre un secondo meccanismo per rimediare
// al primo.
//
// # Perché il confronto passa comunque da hmac.Equal
//
// La ricerca è già per uguaglianza: se lo [Store] restituisce una riga, la sua
// impronta è quella cercata, e il confronto seguente è sempre vero. Resta, e non
// è cerimonia:
//
//   - è il controllo che rende impossibile a un'implementazione dello [Store] —
//     futura, o sbagliata — autenticare restituendo la riga di qualcun altro. Il
//     contratto «solo l'impronta esatta autentica» vive qui, dove si legge e si
//     verifica, non nella fiducia in ogni SELECT scritta altrove;
//   - `hmac.Equal` non termina in anticipo sul primo byte diverso. Su un digest
//     confrontato in Go la differenza è teorica, ma è gratuita, ed è l'unica
//     forma in cui un confronto di segreti va scritto — perché la volta in cui
//     conta si assomiglia molto a questa.
//
// # Perché la revoca ha effetto subito
//
// Non c'è nessuna cache fra la richiesta e la riga: [Key.Active] è valutata sul
// valore appena letto. Una chiave revocata mentre la richiesta precedente era in
// volo è già inutilizzabile alla successiva.
func (s *Service) Authenticate(ctx context.Context, token string, client Client) (auth.User, Key, error) {
	// Il limite si controlla prima della lettura: una SELECT per tentativo è
	// esattamente il lavoro che un tetto deve impedire di far fare gratis.
	if err := allow(s.authIP, client.ipKey()); err != nil {
		return auth.User{}, Key{}, err
	}
	if !LooksLikeToken(token) {
		// Non è nemmeno nella forma giusta: non vale una lettura.
		return auth.User{}, Key{}, ErrInvalidKey
	}

	hash := s.keys.APIKeyHash(token)
	key, err := s.store.KeyByHash(ctx, hash)
	switch {
	case errors.Is(err, ErrNotFound):
		s.logRejected(ctx, client, "chiave_inesistente", "")
		return auth.User{}, Key{}, ErrInvalidKey
	case err != nil:
		return auth.User{}, Key{}, fmt.Errorf("lettura della chiave: %w", err)
	}

	if !hmac.Equal([]byte(key.Hash), []byte(hash)) {
		// Lo Store ha restituito una riga che non corrisponde: è un difetto
		// dell'implementazione, non un tentativo del client. Va segnalato come tale
		// e non deve autenticare nessuno.
		s.log.ErrorContext(ctx, "impronta della chiave non corrispondente: difetto dello Store",
			slog.Any("api_key", key))
		return auth.User{}, Key{}, ErrInvalidKey
	}

	now := s.now()
	if !key.Active(now) {
		reason := "chiave_scaduta"
		if key.Revoked() {
			reason = "chiave_revocata"
		}
		s.logRejected(ctx, client, reason, key.Prefix)
		return auth.User{}, Key{}, ErrInvalidKey
	}

	user, err := s.users.UserByID(ctx, key.UserID)
	switch {
	case errors.Is(err, auth.ErrNotFound):
		// Account cancellato con la chiave ancora in giro.
		s.logRejected(ctx, client, "account_inesistente", key.Prefix)
		return auth.User{}, Key{}, ErrInvalidKey
	case err != nil:
		return auth.User{}, Key{}, fmt.Errorf("lettura dell'account: %w", err)
	}
	if user.Suspended() {
		// Distinto da ErrInvalidKey, come fa il login: per arrivare qui bisogna
		// avere in mano una chiave valida, quindi non si sta rivelando niente a chi
		// non lo sappia già, e il proprietario merita di capire perché.
		return auth.User{}, Key{}, auth.ErrAccountSuspended
	}

	// La chiave è buona: il credito consumato dai tentativi torna a disposizione,
	// così che il traffico legittimo non si autoescluda.
	s.authIP.Reset(client.ipKey())

	if key.LastUsedAt == nil || now.Sub(*key.LastUsedAt) >= touchInterval {
		if err := s.store.TouchKey(ctx, key.ID, now); err != nil {
			// La richiesta è autenticata: non registrarne l'uso è un difetto della
			// traccia, non un motivo per rifiutarla.
			s.log.WarnContext(ctx, "aggiornamento di last_used_at fallito",
				slog.Any("api_key", key), slog.Any("error", err))
		} else {
			key.LastUsedAt = &now
		}
	}
	return user, key, nil
}

// ---------------------------------------------------------------- gestione

// List elenca le chiavi dell'utente, dalla più recente.
//
// Restituisce [Key], che non ha il campo del valore in chiaro perché il valore
// in chiaro non esiste più. Non c'è nessun parametro, nessuna variante e nessun
// ruolo che faccia comparire una chiave dopo la creazione.
func (s *Service) List(ctx context.Context, userID string, includeRevoked bool) ([]Key, error) {
	keys, err := s.store.ListKeys(ctx, userID, includeRevoked)
	if err != nil {
		return nil, fmt.Errorf("elenco delle chiavi: %w", err)
	}
	return keys, nil
}

// Revoke revoca una chiave dell'utente indicato.
//
// L'effetto è immediato per costruzione: l'unica cosa che stava fra la chiave e
// l'autenticazione era la colonna `revoked_at`, e adesso è valorizzata. Non ci
// sono cache da invalidare né scadenze da attendere.
func (s *Service) Revoke(ctx context.Context, userID, keyID string) error {
	err := s.store.RevokeKey(ctx, userID, keyID, s.now())
	switch {
	case errors.Is(err, ErrNotFound):
		return ErrKeyNotFound
	case err != nil:
		return fmt.Errorf("revoca della chiave: %w", err)
	}
	s.log.InfoContext(ctx, "chiave API revocata",
		slog.String("user_id", userID), slog.String("api_key_id", keyID))
	return nil
}

// ------------------------------------------------------------------ interni

// logRejected registra un tentativo rifiutato.
//
// Non contiene il token tentato — nemmeno troncato, nemmeno la sua impronta: un
// log pieno di chiavi tentate è esso stesso un elenco di segreti (SPEC §5). Il
// prefisso di una chiave *esistente* invece c'è, perché non è segreto ed è ciò
// che permette al proprietario di capire quale delle sue chiavi sta fallendo.
func (s *Service) logRejected(ctx context.Context, client Client, reason, prefix string) {
	attrs := []any{
		slog.String("reason", reason),
		slog.String("client_ip", client.ipKey()),
	}
	if prefix != "" {
		attrs = append(attrs, slog.String("api_key_prefix", prefix))
	}
	s.log.InfoContext(ctx, "chiave API rifiutata", attrs...)
}

// allow consuma un gettone e traduce l'esaurimento in un errore.
//
// Restituisce [*auth.RateLimitedError] e non un tipo di questo package: è
// deliberato. La traduzione degli errori in risposte HTTP sta in un solo posto
// (writeAuthError, in internal/httpapi), e la proprietà «un 429 arriva con
// Retry-After qualunque sia l'operazione che l'ha causato» si verifica leggendo
// quella funzione. Un secondo tipo di errore equivalente costringerebbe a un
// secondo ramo identico, che è il modo in cui i due divergono.
func allow(l *ratelimit.Limiter, key string) error {
	if ok, retryAfter := l.Allow(key); !ok {
		return &auth.RateLimitedError{RetryAfter: retryAfter}
	}
	return nil
}

func mergeLimits(l Limits) Limits {
	defaults := DefaultLimits()
	pick := func(rule, fallback ratelimit.Rule) ratelimit.Rule {
		if rule.Burst <= 0 || rule.Window <= 0 {
			return fallback
		}
		return rule
	}
	return Limits{
		AuthPerIP:     pick(l.AuthPerIP, defaults.AuthPerIP),
		CreatePerUser: pick(l.CreatePerUser, defaults.CreatePerUser),
	}
}

func joinScopes(scopes []Scope) string {
	parts := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		parts = append(parts, string(scope))
	}
	return strings.Join(parts, ", ")
}
