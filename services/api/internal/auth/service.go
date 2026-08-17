// Package auth implementa l'autenticazione degli utenti di Postqron (R14):
// registrazione, login, logout, gestione delle sessioni e recupero password.
//
// Il package contiene la logica e non conosce né HTTP né PostgreSQL: le rotte
// stanno in internal/httpapi, la persistenza dietro l'interfaccia [Store] con
// l'implementazione in internal/authpg. È la separazione che permette di
// verificare i comportamenti difficili — enumerazione degli account, tempi di
// risposta, rate limiting — con test che non hanno bisogno di un database.
//
// # Le tre proprietà che il package deve garantire
//
// **1. Le password non sono ricostruibili da un dump.** Argon2id con parametri
// espliciti e motivati; vedi [Argon2idParams].
//
// **2. Non si può sapere se un indirizzo è registrato.** Registrazione, login e
// recupero password rispondono con lo stesso corpo, lo stesso status e —
// soprattutto — nello stesso tempo, sia che l'account esista sia che non
// esista. Le contromisure sono tre, una per canale:
//
//   - *corpo e status*: la registrazione risponde sempre 202 e il login sempre
//     401, senza sfumature;
//   - *tempo, sul login*: il percorso «utente inesistente» esegue comunque un
//     Argon2id completo contro un hash civetta ([Hasher.VerifyDecoy]). È il
//     canale che si dimentica: senza, l'assenza del confronto rende la risposta
//     misurabilmente più veloce;
//   - *tempo, sul recupero password*: lì non c'è nessun hash che domini il
//     tempo, e la differenza fra «cerco, trovo, scrivo il token, invio» e
//     «cerco, non trovo» sarebbe visibile. Il rimedio non è bilanciare i due
//     rami ma toglierli entrambi dal percorso della risposta: la risposta parte
//     subito e il lavoro si fa dopo ([Service.RequestPasswordReset]).
//
// **3. I tentativi sono limitati.** Vedi internal/ratelimit e [Limits].
package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/ratelimit"
)

// -------------------------------------------------------------------- errori

var (
	// ErrInvalidCredentials è l'unico esito di un login mancato, qualunque sia
	// la causa reale: indirizzo inesistente, indirizzo malformato, password
	// sbagliata, account senza password perché nato da un provider esterno.
	// Distinguere sarebbe enumerare.
	ErrInvalidCredentials = errors.New("credenziali non valide")

	// ErrAccountSuspended è restituito quando la password è corretta ma
	// l'account è sospeso.
	//
	// Distinguerlo non è una fuga di informazione: per arrivare qui bisogna già
	// conoscere la password, quindi non si sta rivelando nulla a chi non lo sa
	// già. Il rovescio — rispondere «credenziali non valide» a un utente sospeso
	// — sarebbe una segnalazione falsa su cui il supporto perderebbe tempo.
	ErrAccountSuspended = errors.New("account sospeso")

	// ErrInvalidToken copre un token monouso inesistente, scaduto, già
	// consumato o di scopo diverso da quello atteso.
	ErrInvalidToken = errors.New("token non valido o scaduto")

	// ErrInvalidEmail è restituito quando l'indirizzo non ha una forma
	// plausibile. Rivelarlo è innocuo: chi ha scritto l'indirizzo sa già com'è
	// fatto, e un indirizzo malformato non può appartenere a nessun account.
	ErrInvalidEmail = errors.New("indirizzo email non valido")

	// ErrUnauthenticated indica che la sessione non esiste, è scaduta o è stata
	// revocata.
	ErrUnauthenticated = errors.New("sessione assente o non più valida")

	// ErrSessionNotFound è restituito quando la sessione da revocare non
	// appartiene all'utente o non esiste.
	ErrSessionNotFound = errors.New("sessione non trovata")
)

// RateLimitedError è restituito quando una chiave ha esaurito il suo credito.
type RateLimitedError struct {
	// RetryAfter è l'attesa dopo la quale un tentativo tornerà possibile.
	RetryAfter time.Duration
}

func (e *RateLimitedError) Error() string {
	return fmt.Sprintf("troppi tentativi, riprovare fra %s", e.RetryAfter)
}

// -------------------------------------------------------------- durate e limiti

// Durate di riferimento. Sono nel codice e non in configurazione perché sono
// scelte di sicurezza, non parametri d'esercizio: cambiarle è una modifica da
// discutere, non una variabile d'ambiente da girare in produzione.
const (
	// DefaultSessionTTL è la vita massima di una sessione, indipendentemente
	// dall'uso. Trenta giorni sono un compromesso: Postqron è uno strumento che
	// si consulta a intermittenza, e un mese di «resta collegato» è quanto un
	// utente si aspetta da una dashboard operativa.
	DefaultSessionTTL = 30 * 24 * time.Hour

	// DefaultSessionIdleTTL è la vita di una sessione non usata. È distinta
	// dalla scadenza assoluta perché sono due rischi diversi: la scadenza
	// assoluta limita la finestra di un token rubato, l'inattività chiude le
	// sessioni dimenticate su un computer altrui.
	DefaultSessionIdleTTL = 14 * 24 * time.Hour

	// DefaultEmailVerificationTTL è la validità del link di conferma
	// dell'indirizzo. Lungo, perché un'email di benvenuto si legge anche il
	// giorno dopo.
	DefaultEmailVerificationTTL = 48 * time.Hour

	// DefaultPasswordResetTTL è la validità del link di recupero password.
	// Corto, perché è il token più potente che il sistema spedisce: un'ora è il
	// tempo che serve a leggere un'email, non a lasciarla in una casella
	// condivisa.
	DefaultPasswordResetTTL = time.Hour

	// touchInterval è la granularità con cui si aggiorna `last_used_at`.
	//
	// Senza soglia, ogni richiesta autenticata sarebbe anche una scrittura. Con
	// cinque minuti la scadenza per inattività resta esatta al minuto e le
	// scritture diventano una frazione delle letture.
	touchInterval = 5 * time.Minute

	// backgroundTimeout limita il lavoro svolto dopo la risposta (invio delle
	// email, emissione dei token di recupero).
	backgroundTimeout = 30 * time.Second
)

// Limits raccoglie le regole di rate limiting dell'autenticazione.
//
// Sono due dimensioni per ogni operazione sensibile, e servono a cose diverse:
// la chiave per indirizzo IP ferma chi prova molte password su molti account, la
// chiave per indirizzo email ferma chi prova molte password su un account solo
// da molti indirizzi. Una sola delle due lascia aperta l'altra metà.
type Limits struct {
	// LoginPerIP limita i tentativi di login falliti da uno stesso indirizzo.
	LoginPerIP ratelimit.Rule
	// LoginPerAccount limita i tentativi falliti su uno stesso indirizzo email.
	LoginPerAccount ratelimit.Rule
	// RegisterPerIP limita le registrazioni da uno stesso indirizzo.
	RegisterPerIP ratelimit.Rule
	// PasswordResetPerIP limita le richieste di recupero da uno stesso
	// indirizzo.
	PasswordResetPerIP ratelimit.Rule
	// PasswordResetPerAccount limita le richieste di recupero verso uno stesso
	// indirizzo email. È il limite che impedisce di usare Postqron per
	// bombardare di email la casella di qualcun altro.
	PasswordResetPerAccount ratelimit.Rule
	// TokenPerIP limita i tentativi di usare un token monouso (conferma
	// indirizzo, reimpostazione password) da uno stesso indirizzo. I token non
	// sono indovinabili, ma un tetto evita che qualcuno li usi come oracolo o
	// come carico.
	TokenPerIP ratelimit.Rule
}

// DefaultLimits sono i limiti applicati se non se ne indicano altri.
//
// I valori sono scelti per essere invisibili a un utente reale e stretti per un
// attaccante. Cinque login falliti in un quarto d'ora su un account sono già
// più di quanto sbagli chi ha dimenticato quale password ha usato; venti per
// indirizzo IP lasciano lavorare un ufficio dietro un NAT senza aprire la porta
// a una forzatura seria — con questo tetto, provare mille password su un
// account richiede più di due giorni.
func DefaultLimits() Limits {
	return Limits{
		LoginPerIP:              ratelimit.Rule{Burst: 20, Window: 15 * time.Minute},
		LoginPerAccount:         ratelimit.Rule{Burst: 5, Window: 15 * time.Minute},
		RegisterPerIP:           ratelimit.Rule{Burst: 5, Window: time.Hour},
		PasswordResetPerIP:      ratelimit.Rule{Burst: 5, Window: time.Hour},
		PasswordResetPerAccount: ratelimit.Rule{Burst: 3, Window: time.Hour},
		TokenPerIP:              ratelimit.Rule{Burst: 10, Window: 15 * time.Minute},
	}
}

// ------------------------------------------------------------------- servizio

// Client descrive la provenienza di una richiesta.
//
// Serve al rate limiting e all'elenco «dove sono collegato» delle sessioni. È
// un parametro esplicito e non un valore estratto dal contesto perché chi chiama
// il Service deve accorgersi che l'indirizzo va passato: un Client vuoto
// funziona, ma fa condividere lo stesso secchio a tutto il mondo, e questo va
// visto in fase di scrittura del codice, non in produzione.
type Client struct {
	// IP è l'indirizzo del chiamante. Lo zero value significa «sconosciuto».
	IP netip.Addr
	// UserAgent è la stringa dichiarata dal client, troncata da chi la fornisce.
	UserAgent string
}

func (c Client) ipKey() string {
	if !c.IP.IsValid() {
		return "sconosciuto"
	}
	return c.IP.Unmap().String()
}

func (c Client) addr() *netip.Addr {
	if !c.IP.IsValid() {
		return nil
	}
	unmapped := c.IP.Unmap()
	return &unmapped
}

// PasswordHasher è ciò che il [Service] usa per le password.
//
// È un'interfaccia per la stessa ragione di [Store] e [Mailer]: la proprietà «il
// percorso dell'utente inesistente esegue comunque un hash completo» va provata
// contando le operazioni, non misurando i millisecondi. Un test che si affida
// solo al cronometro è un test che a volte passa. [Hasher] è l'unica
// implementazione d'esercizio.
type PasswordHasher interface {
	// Hash produce l'hash di una password.
	Hash(ctx context.Context, password string) (string, error)
	// Verify confronta una password con un hash memorizzato e segnala se l'hash
	// è stato prodotto con parametri di costo obsoleti.
	Verify(ctx context.Context, encoded, password string) (ok bool, stale bool, err error)
	// VerifyDecoy paga il costo di una verifica quando non c'è nulla da
	// verificare. Vedi [Hasher.VerifyDecoy].
	VerifyDecoy(ctx context.Context, password string) error
}

// Options configura il [Service]. Store, Hasher, Keyring e Mailer sono
// obbligatori; il resto ha un default.
type Options struct {
	Store   Store
	Hasher  PasswordHasher
	Keyring Keyring
	Mailer  Mailer
	Logger  *slog.Logger

	// Now sostituisce l'orologio. Serve ai test sulle scadenze.
	Now func() time.Time

	SessionTTL           time.Duration
	SessionIdleTTL       time.Duration
	EmailVerificationTTL time.Duration
	PasswordResetTTL     time.Duration

	// Limits sostituisce [DefaultLimits]. Il campo zero usa i default; una
	// singola regola con Burst zero usa il default di quella regola.
	Limits Limits
}

// Service è l'autenticazione. Va costruito con [NewService].
type Service struct {
	store   Store
	hasher  PasswordHasher
	keys    Keyring
	mailer  Mailer
	log     *slog.Logger
	now     func() time.Time
	limits  Limits
	verify  time.Duration
	reset   time.Duration
	session time.Duration
	idle    time.Duration

	loginIP      *ratelimit.Limiter
	loginAccount *ratelimit.Limiter
	registerIP   *ratelimit.Limiter
	resetIP      *ratelimit.Limiter
	resetAccount *ratelimit.Limiter
	tokenIP      *ratelimit.Limiter

	// background tiene traccia del lavoro svolto dopo la risposta, perché
	// l'arresto graceful lo possa attendere invece di troncarlo.
	background sync.WaitGroup
}

// NewService costruisce il servizio.
func NewService(opts Options) (*Service, error) {
	if opts.Store == nil {
		return nil, errors.New("auth: Store è obbligatorio")
	}
	if opts.Hasher == nil {
		return nil, errors.New("auth: Hasher è obbligatorio")
	}
	if !opts.Keyring.valid() {
		return nil, errors.New("auth: Keyring non inizializzato")
	}
	if opts.Mailer == nil {
		return nil, errors.New("auth: Mailer è obbligatorio; usare LogMailer finché #419 non è pronta")
	}

	s := &Service{
		store:   opts.Store,
		hasher:  opts.Hasher,
		keys:    opts.Keyring,
		mailer:  opts.Mailer,
		log:     opts.Logger,
		now:     opts.Now,
		limits:  mergeLimits(opts.Limits),
		verify:  durationOr(opts.EmailVerificationTTL, DefaultEmailVerificationTTL),
		reset:   durationOr(opts.PasswordResetTTL, DefaultPasswordResetTTL),
		session: durationOr(opts.SessionTTL, DefaultSessionTTL),
		idle:    durationOr(opts.SessionIdleTTL, DefaultSessionIdleTTL),
	}
	if s.log == nil {
		s.log = slog.Default()
	}
	if s.now == nil {
		s.now = time.Now
	}
	if s.idle > s.session {
		// Una scadenza per inattività più lunga di quella assoluta non ha
		// effetto: è quasi certamente un errore di configurazione.
		return nil, fmt.Errorf("auth: SessionIdleTTL (%s) supera SessionTTL (%s)", s.idle, s.session)
	}

	clock := ratelimit.WithClock(s.now)
	var err error
	if s.loginIP, err = ratelimit.New(s.limits.LoginPerIP, clock); err != nil {
		return nil, fmt.Errorf("auth: LoginPerIP: %w", err)
	}
	if s.loginAccount, err = ratelimit.New(s.limits.LoginPerAccount, clock); err != nil {
		return nil, fmt.Errorf("auth: LoginPerAccount: %w", err)
	}
	if s.registerIP, err = ratelimit.New(s.limits.RegisterPerIP, clock); err != nil {
		return nil, fmt.Errorf("auth: RegisterPerIP: %w", err)
	}
	if s.resetIP, err = ratelimit.New(s.limits.PasswordResetPerIP, clock); err != nil {
		return nil, fmt.Errorf("auth: PasswordResetPerIP: %w", err)
	}
	if s.resetAccount, err = ratelimit.New(s.limits.PasswordResetPerAccount, clock); err != nil {
		return nil, fmt.Errorf("auth: PasswordResetPerAccount: %w", err)
	}
	if s.tokenIP, err = ratelimit.New(s.limits.TokenPerIP, clock); err != nil {
		return nil, fmt.Errorf("auth: TokenPerIP: %w", err)
	}
	return s, nil
}

// SessionIdleTTL è la scadenza per inattività applicata dal servizio. La
// espone perché [Session.Active] ne ha bisogno anche fuori dal package.
func (s *Service) SessionIdleTTL() time.Duration { return s.idle }

// Wait attende il lavoro avviato dopo le risposte già inviate.
//
// I test la usano per osservare un invio che, per costruzione, avviene fuori dal
// percorso della richiesta.
func (s *Service) Wait() { s.background.Wait() }

// Shutdown attende il lavoro in corso, o il contesto, quale dei due arriva
// prima. Va chiamata durante l'arresto graceful del servizio.
func (s *Service) Shutdown(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		s.background.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// ---------------------------------------------------------------- registrazione

// RegisterInput sono i dati di una registrazione.
type RegisterInput struct {
	Email    string
	Password string
	FullName string
	Client   Client
}

// Register crea un account.
//
// **Non dice mai se l'indirizzo era già registrato.** Le due strade —
// indirizzo libero, indirizzo preso — fanno lo stesso lavoro osservabile:
// validano, calcolano l'hash Argon2id della password (anche quando è già certo
// che non servirà: è il costo che rende i due tempi indistinguibili, ed è
// deliberato), tentano l'inserimento, e mettono in coda un'email diversa. La
// differenza sta solo in quale template riceve il proprietario dell'indirizzo,
// che è l'unica persona autorizzata a saperlo.
//
// Errori possibili: [ErrInvalidEmail], gli errori di policy sulla password,
// [*RateLimitedError]. Nessuno dei tre dipende dall'esistenza dell'account.
func (s *Service) Register(ctx context.Context, in RegisterInput) error {
	email, err := NormalizeEmail(in.Email)
	if err != nil {
		return err
	}
	if err := ValidatePassword(in.Password); err != nil {
		return err
	}
	if err := allow(s.registerIP, in.Client.ipKey()); err != nil {
		return err
	}

	hash, err := s.hasher.Hash(ctx, in.Password)
	if err != nil {
		return fmt.Errorf("hashing della password: %w", err)
	}

	user, err := s.store.CreateUser(ctx, email, hash, strings.TrimSpace(in.FullName))
	switch {
	case errors.Is(err, ErrEmailTaken):
		// L'indirizzo ha già un account. L'unico modo lecito di dirlo è dirlo al
		// proprietario, per email: al chiamante si risponde come se la
		// registrazione fosse andata a buon fine.
		s.log.InfoContext(ctx, "registrazione su indirizzo già presente",
			slog.String("client_ip", in.Client.ipKey()))
		s.dispatch(ctx, Message{Kind: KindRegistrationAttempt, To: in.Email})
		return nil
	case err != nil:
		return fmt.Errorf("creazione dell'account: %w", err)
	}

	s.log.InfoContext(ctx, "account registrato",
		slog.String("user_id", user.ID),
		slog.String("client_ip", in.Client.ipKey()))

	token, expires, err := s.issueToken(ctx, user.ID, PurposeEmailVerification, s.verify, in.Client)
	if err != nil {
		// L'account esiste: fallire adesso renderebbe la registrazione un
		// errore quando non lo è. Si registra e si va avanti — l'utente può
		// chiedere di nuovo la conferma dopo il login.
		s.log.ErrorContext(ctx, "token di conferma indirizzo non emesso",
			slog.String("user_id", user.ID), slog.Any("error", err))
		return nil
	}

	s.dispatch(ctx, Message{
		Kind:      KindEmailVerification,
		To:        user.Email,
		UserID:    user.ID,
		Token:     token,
		ExpiresAt: expires,
	})
	return nil
}

// ---------------------------------------------------------------------- login

// LoginInput sono i dati di un tentativo di accesso.
type LoginInput struct {
	Email    string
	Password string
	Client   Client
}

// LoginResult è l'esito di un accesso riuscito.
type LoginResult struct {
	User    User
	Session Session
	// Token è il valore da consegnare al client. È l'unico momento in cui
	// esiste in chiaro: nel database ne resta solo l'HMAC.
	Token string
}

// Login verifica le credenziali e apre una sessione.
//
// Il percorso «indirizzo inesistente» esegue comunque un Argon2id completo
// contro un hash civetta. È la parte che si dimentica della protezione contro
// l'enumerazione: senza, la risposta arriverebbe in un paio di millisecondi
// invece che in un centinaio, e la differenza è misurabile da fuori con un
// campione di poche decine di richieste.
func (s *Service) Login(ctx context.Context, in LoginInput) (LoginResult, error) {
	ipKey := in.Client.ipKey()
	accountKey := emailKey(in.Email)

	// I limiti si controllano prima dell'hashing, non dopo: un Argon2id per
	// tentativo è anche il modo più semplice di far consumare CPU al servizio.
	// Entrambe le chiavi sono contate allo stesso modo per indirizzi esistenti e
	// inventati, quindi un 429 non dice nulla su chi esiste.
	if err := allow(s.loginIP, ipKey); err != nil {
		return LoginResult{}, err
	}
	if err := allow(s.loginAccount, accountKey); err != nil {
		return LoginResult{}, err
	}

	email, emailErr := NormalizeEmail(in.Email)
	if emailErr != nil {
		// Un indirizzo malformato non può appartenere a nessun account: saltare
		// l'hash qui non rivela niente che il chiamante non sappia già, ed evita
		// di trasformare richieste spazzatura in lavoro per la CPU.
		return LoginResult{}, ErrInvalidCredentials
	}

	user, err := s.store.UserByEmail(ctx, email)
	switch {
	case errors.Is(err, ErrNotFound):
		if err := s.hasher.VerifyDecoy(ctx, in.Password); err != nil {
			return LoginResult{}, err
		}
		s.logFailedLogin(ctx, in.Client, "utente_inesistente")
		return LoginResult{}, ErrInvalidCredentials
	case err != nil:
		return LoginResult{}, fmt.Errorf("lettura dell'account: %w", err)
	}

	if user.PasswordHash == "" {
		// Account senza password: nato da un provider esterno, oppure con l'hash
		// azzerato. Costa quanto una verifica vera, come sopra.
		if err := s.hasher.VerifyDecoy(ctx, in.Password); err != nil {
			return LoginResult{}, err
		}
		s.logFailedLogin(ctx, in.Client, "account_senza_password")
		return LoginResult{}, ErrInvalidCredentials
	}

	ok, stale, err := s.hasher.Verify(ctx, user.PasswordHash, in.Password)
	if err != nil {
		return LoginResult{}, fmt.Errorf("verifica della password: %w", err)
	}
	if !ok {
		s.logFailedLogin(ctx, in.Client, "password_errata")
		return LoginResult{}, ErrInvalidCredentials
	}

	// Da qui in poi la password è quella giusta: quello che si risponde non può
	// più aiutare un attaccante a scoprire quali account esistono.
	if user.Suspended() {
		return LoginResult{}, ErrAccountSuspended
	}

	s.loginIP.Reset(ipKey)
	s.loginAccount.Reset(accountKey)

	if stale {
		// Unico momento in cui la password in chiaro esiste: se i parametri di
		// costo sono stati alzati, l'hash si rigenera adesso o mai più.
		s.rehash(ctx, user, in.Password)
	}

	result, err := s.openSession(ctx, user, in.Client)
	if err != nil {
		return LoginResult{}, err
	}

	now := s.now()
	if err := s.store.TouchLastLogin(ctx, user.ID, now); err != nil {
		// L'accesso è avvenuto: non registrarne la data è un difetto della
		// traccia, non un motivo per rifiutare il login.
		s.log.WarnContext(ctx, "aggiornamento di last_login_at fallito",
			slog.String("user_id", user.ID), slog.Any("error", err))
	}
	s.log.InfoContext(ctx, "accesso riuscito",
		slog.String("user_id", user.ID),
		slog.String("session_id", result.Session.ID),
		slog.String("client_ip", in.Client.ipKey()))
	return result, nil
}

// ------------------------------------------------------------------- sessioni

// Authenticate risolve un token di sessione nell'utente e nella sessione.
//
// Applica entrambe le scadenze e la revoca, e aggiorna `last_used_at` non più di
// una volta ogni [touchInterval].
func (s *Service) Authenticate(ctx context.Context, token string) (User, Session, error) {
	if token == "" {
		return User{}, Session{}, ErrUnauthenticated
	}

	session, err := s.store.SessionByTokenHash(ctx, s.keys.SessionHash(token))
	switch {
	case errors.Is(err, ErrNotFound):
		return User{}, Session{}, ErrUnauthenticated
	case err != nil:
		return User{}, Session{}, fmt.Errorf("lettura della sessione: %w", err)
	}

	now := s.now()
	if !session.Active(now, s.idle) {
		return User{}, Session{}, ErrUnauthenticated
	}

	user, err := s.store.UserByID(ctx, session.UserID)
	switch {
	case errors.Is(err, ErrNotFound):
		// Account cancellato con la sessione ancora in giro.
		return User{}, Session{}, ErrUnauthenticated
	case err != nil:
		return User{}, Session{}, fmt.Errorf("lettura dell'account: %w", err)
	}
	if user.Suspended() {
		return User{}, Session{}, ErrAccountSuspended
	}

	if now.Sub(session.LastUsedAt) >= touchInterval {
		if err := s.store.TouchSession(ctx, session.ID, now); err != nil {
			s.log.WarnContext(ctx, "aggiornamento di last_used_at fallito",
				slog.String("session_id", session.ID), slog.Any("error", err))
		} else {
			session.LastUsedAt = now
		}
	}
	return user, session, nil
}

// Logout revoca la sessione a cui il token appartiene.
//
// Un token già scaduto o inesistente non è un errore: il risultato voluto —
// «questa sessione non vale più» — è già vero, e distinguere i due casi darebbe
// a chi ha in mano un token la conferma che era valido.
func (s *Service) Logout(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	err := s.store.RevokeSessionByTokenHash(ctx, s.keys.SessionHash(token), s.now())
	if err != nil && !errors.Is(err, ErrNotFound) {
		return fmt.Errorf("revoca della sessione: %w", err)
	}
	return nil
}

// ListSessions elenca le sessioni vive dell'utente, dalla più recente.
func (s *Service) ListSessions(ctx context.Context, userID string) ([]Session, error) {
	sessions, err := s.store.ListSessions(ctx, userID, s.now())
	if err != nil {
		return nil, fmt.Errorf("elenco delle sessioni: %w", err)
	}
	// Lo Store filtra già revocate e scadute in modo assoluto; l'inattività
	// dipende da una durata che conosce solo il Service.
	now := s.now()
	live := make([]Session, 0, len(sessions))
	for _, session := range sessions {
		if session.Active(now, s.idle) {
			live = append(live, session)
		}
	}
	return live, nil
}

// RevokeSession chiude una sessione dell'utente indicato.
func (s *Service) RevokeSession(ctx context.Context, userID, sessionID string) error {
	err := s.store.RevokeSession(ctx, userID, sessionID, s.now())
	switch {
	case errors.Is(err, ErrNotFound):
		return ErrSessionNotFound
	case err != nil:
		return fmt.Errorf("revoca della sessione: %w", err)
	}
	s.log.InfoContext(ctx, "sessione revocata",
		slog.String("user_id", userID), slog.String("session_id", sessionID))
	return nil
}

// RevokeOtherSessions chiude tutte le sessioni dell'utente tranne quella
// indicata, e restituisce quante ne ha chiuse.
func (s *Service) RevokeOtherSessions(ctx context.Context, userID, keepSessionID string) (int, error) {
	n, err := s.store.RevokeUserSessions(ctx, userID, keepSessionID, s.now())
	if err != nil {
		return 0, fmt.Errorf("revoca delle sessioni: %w", err)
	}
	s.log.InfoContext(ctx, "sessioni revocate",
		slog.String("user_id", userID), slog.Int("count", n))
	return n, nil
}

// ------------------------------------------------------- recupero password

// RequestPasswordReset avvia il recupero password.
//
// Risponde sempre nello stesso modo e nello stesso tempo, e per farlo non
// bilancia i due rami: li sposta entrambi *dopo* la risposta. La ricerca
// dell'account, la scrittura del token e la messa in coda dell'email avvengono
// in una goroutine con un contesto proprio; il chiamante torna subito, prima
// che si sappia se l'indirizzo esiste.
//
// È l'unico modo robusto: qui non c'è un Argon2id da cento millisecondi che
// copra la differenza fra una SELECT che trova e una che non trova, e un
// `time.Sleep` di compensazione sarebbe un numero inventato — sbagliato appena
// il database rallenta o la macchina cambia.
//
// I limiti, invece, si applicano prima: un 429 deve essere vero al momento in
// cui lo si dà, e non rivela nulla perché conta gli indirizzi inventati come
// quelli registrati.
func (s *Service) RequestPasswordReset(ctx context.Context, email string, client Client) error {
	if err := allow(s.resetIP, client.ipKey()); err != nil {
		return err
	}
	if err := allow(s.resetAccount, emailKey(email)); err != nil {
		return err
	}

	// Un indirizzo malformato non arriva nemmeno alla goroutine: non esiste
	// account che possa averlo, e la risposta al client è comunque la stessa.
	normalized, err := NormalizeEmail(email)
	if err != nil {
		s.log.InfoContext(ctx, "recupero password su indirizzo malformato",
			slog.String("client_ip", client.ipKey()))
		return nil
	}

	s.run(ctx, func(ctx context.Context) {
		user, err := s.store.UserByEmail(ctx, normalized)
		switch {
		case errors.Is(err, ErrNotFound):
			s.log.InfoContext(ctx, "recupero password per indirizzo senza account",
				slog.String("client_ip", client.ipKey()))
			return
		case err != nil:
			s.log.ErrorContext(ctx, "recupero password: lettura dell'account fallita",
				slog.Any("error", err))
			return
		}

		// Un account sospeso non riceve un link di reimpostazione: reimpostare
		// la password non gli restituirebbe l'accesso, e il messaggio sarebbe
		// fuorviante. Al chiamante non cambia niente.
		if user.Suspended() {
			s.log.InfoContext(ctx, "recupero password su account sospeso",
				slog.String("user_id", user.ID))
			return
		}

		token, expires, err := s.issueToken(ctx, user.ID, PurposePasswordReset, s.reset, client)
		if err != nil {
			s.log.ErrorContext(ctx, "emissione del token di recupero fallita",
				slog.String("user_id", user.ID), slog.Any("error", err))
			return
		}

		s.log.InfoContext(ctx, "recupero password richiesto",
			slog.String("user_id", user.ID), slog.String("client_ip", client.ipKey()))

		s.send(ctx, Message{
			Kind:      KindPasswordReset,
			To:        user.Email,
			UserID:    user.ID,
			Token:     token,
			ExpiresAt: expires,
		})
	})
	return nil
}

// ResetPassword consuma un token di recupero e imposta la password nuova.
//
// Tutte le sessioni dell'utente vengono chiuse: se il recupero serviva perché
// qualcun altro aveva l'accesso, lasciargli una sessione aperta vanificherebbe
// l'operazione.
func (s *Service) ResetPassword(ctx context.Context, token, newPassword string, client Client) error {
	if err := allow(s.tokenIP, client.ipKey()); err != nil {
		return err
	}
	// La policy si applica prima di consumare il token: una password troppo
	// corta non deve bruciare il link, o l'utente dovrebbe ricominciare da capo
	// per un errore di battitura.
	if err := ValidatePassword(newPassword); err != nil {
		return err
	}
	if token == "" {
		return ErrInvalidToken
	}

	now := s.now()
	consumed, err := s.store.ConsumeUserToken(ctx, s.keys.UserTokenHash(token), PurposePasswordReset, now)
	switch {
	case errors.Is(err, ErrNotFound):
		return ErrInvalidToken
	case err != nil:
		return fmt.Errorf("consumo del token: %w", err)
	}

	user, err := s.store.UserByID(ctx, consumed.UserID)
	switch {
	case errors.Is(err, ErrNotFound):
		return ErrInvalidToken
	case err != nil:
		return fmt.Errorf("lettura dell'account: %w", err)
	}

	hash, err := s.hasher.Hash(ctx, newPassword)
	if err != nil {
		return fmt.Errorf("hashing della password: %w", err)
	}
	if err := s.store.UpdatePasswordHash(ctx, user.ID, hash); err != nil {
		return fmt.Errorf("aggiornamento della password: %w", err)
	}

	if _, err := s.store.RevokeUserSessions(ctx, user.ID, "", now); err != nil {
		return fmt.Errorf("revoca delle sessioni: %w", err)
	}
	// Gli altri token di recupero pendenti non devono sopravvivere a una
	// reimpostazione riuscita.
	if _, err := s.store.RevokeUserTokens(ctx, user.ID, PurposePasswordReset, now); err != nil {
		s.log.WarnContext(ctx, "revoca dei token di recupero residui fallita",
			slog.String("user_id", user.ID), slog.Any("error", err))
	}

	// Aver ricevuto il link dimostra il controllo della casella: è la stessa
	// prova che chiede la conferma dell'indirizzo, e chiederla due volte non
	// aggiunge nulla.
	if !user.EmailVerified() {
		if err := s.store.MarkEmailVerified(ctx, user.ID, now); err != nil {
			s.log.WarnContext(ctx, "conferma implicita dell'indirizzo fallita",
				slog.String("user_id", user.ID), slog.Any("error", err))
		}
	}

	s.log.InfoContext(ctx, "password reimpostata",
		slog.String("user_id", user.ID), slog.String("client_ip", client.ipKey()))

	s.dispatch(ctx, Message{Kind: KindPasswordChanged, To: user.Email, UserID: user.ID})
	return nil
}

// ChangePassword cambia la password di un utente già autenticato.
//
// Richiede la password corrente: senza, una sessione rubata basterebbe a
// prendere possesso dell'account in modo definitivo. Le altre sessioni vengono
// chiuse e quella corrente ottiene un token nuovo, così che il token vecchio —
// che potrebbe essere in mano a qualcun altro — non valga più.
func (s *Service) ChangePassword(ctx context.Context, user User, session Session, current, next string) (LoginResult, error) {
	if err := ValidatePassword(next); err != nil {
		return LoginResult{}, err
	}
	// Lo stesso secchio del login: chi indovina la password corrente a tentativi
	// da una sessione rubata incontra lo stesso tetto.
	accountKey := emailKey(user.Email)
	if err := allow(s.loginAccount, accountKey); err != nil {
		return LoginResult{}, err
	}

	ok, _, err := s.hasher.Verify(ctx, user.PasswordHash, current)
	if err != nil {
		return LoginResult{}, fmt.Errorf("verifica della password: %w", err)
	}
	if !ok {
		return LoginResult{}, ErrInvalidCredentials
	}
	s.loginAccount.Reset(accountKey)

	hash, err := s.hasher.Hash(ctx, next)
	if err != nil {
		return LoginResult{}, fmt.Errorf("hashing della password: %w", err)
	}
	if err := s.store.UpdatePasswordHash(ctx, user.ID, hash); err != nil {
		return LoginResult{}, fmt.Errorf("aggiornamento della password: %w", err)
	}

	now := s.now()
	if _, err := s.store.RevokeUserSessions(ctx, user.ID, "", now); err != nil {
		return LoginResult{}, fmt.Errorf("revoca delle sessioni: %w", err)
	}

	client := Client{UserAgent: session.UserAgent}
	if session.IPAddress != nil {
		client.IP = *session.IPAddress
	}
	result, err := s.openSession(ctx, user, client)
	if err != nil {
		return LoginResult{}, err
	}

	s.log.InfoContext(ctx, "password cambiata",
		slog.String("user_id", user.ID), slog.String("session_id", result.Session.ID))

	s.dispatch(ctx, Message{Kind: KindPasswordChanged, To: user.Email, UserID: user.ID})
	return result, nil
}

// ------------------------------------------------------ conferma indirizzo

// VerifyEmail consuma un token di conferma e segna l'indirizzo come verificato.
func (s *Service) VerifyEmail(ctx context.Context, token string, client Client) error {
	if err := allow(s.tokenIP, client.ipKey()); err != nil {
		return err
	}
	if token == "" {
		return ErrInvalidToken
	}

	now := s.now()
	consumed, err := s.store.ConsumeUserToken(ctx, s.keys.UserTokenHash(token), PurposeEmailVerification, now)
	switch {
	case errors.Is(err, ErrNotFound):
		return ErrInvalidToken
	case err != nil:
		return fmt.Errorf("consumo del token: %w", err)
	}

	if err := s.store.MarkEmailVerified(ctx, consumed.UserID, now); err != nil {
		return fmt.Errorf("conferma dell'indirizzo: %w", err)
	}
	s.log.InfoContext(ctx, "indirizzo confermato", slog.String("user_id", consumed.UserID))
	return nil
}

// ResendEmailVerification emette un nuovo token di conferma per un utente
// autenticato.
//
// È il rimedio all'email di benvenuto persa. Richiede una sessione, quindi non
// c'è nessuna enumerazione da evitare: chi la chiama ha già dimostrato di essere
// il proprietario dell'account.
func (s *Service) ResendEmailVerification(ctx context.Context, user User, client Client) error {
	// Lo stesso secchio del recupero password, e deliberatamente: il limite che
	// conta qui non è «quante volte chiedi» ma «quante email arrivano a questa
	// casella», e le due operazioni scrivono allo stesso indirizzo. Due contatori
	// separati raddoppierebbero il volume che si può far recapitare a qualcuno.
	if err := allow(s.resetAccount, emailKey(user.Email)); err != nil {
		return err
	}
	if user.EmailVerified() {
		return nil
	}

	token, expires, err := s.issueToken(ctx, user.ID, PurposeEmailVerification, s.verify, client)
	if err != nil {
		return fmt.Errorf("emissione del token di conferma: %w", err)
	}
	s.dispatch(ctx, Message{
		Kind:      KindEmailVerification,
		To:        user.Email,
		UserID:    user.ID,
		Token:     token,
		ExpiresAt: expires,
	})
	return nil
}

// ------------------------------------------------------------------- interni

// openSession genera il token, lo registra e restituisce l'esito.
func (s *Service) openSession(ctx context.Context, user User, client Client) (LoginResult, error) {
	token, err := newToken()
	if err != nil {
		return LoginResult{}, err
	}
	now := s.now()
	session, err := s.store.CreateSession(ctx, Session{
		UserID:     user.ID,
		TokenHash:  s.keys.SessionHash(token),
		CreatedAt:  now,
		LastUsedAt: now,
		ExpiresAt:  now.Add(s.session),
		IPAddress:  client.addr(),
		UserAgent:  client.UserAgent,
	})
	if err != nil {
		return LoginResult{}, fmt.Errorf("creazione della sessione: %w", err)
	}
	return LoginResult{User: user, Session: session, Token: token}, nil
}

// issueToken emette un token monouso, invalidando quelli pendenti dello stesso
// scopo: un link nuovo deve rendere inutile il precedente, altrimenti «chiedi un
// altro link» moltiplica i segreti validi invece di sostituirli.
func (s *Service) issueToken(
	ctx context.Context, userID string, purpose TokenPurpose, ttl time.Duration, client Client,
) (string, time.Time, error) {
	now := s.now()
	if _, err := s.store.RevokeUserTokens(ctx, userID, purpose, now); err != nil {
		return "", time.Time{}, fmt.Errorf("invalidazione dei token precedenti: %w", err)
	}

	token, err := newToken()
	if err != nil {
		return "", time.Time{}, err
	}
	expires := now.Add(ttl)
	if _, err := s.store.CreateUserToken(ctx, UserToken{
		UserID:      userID,
		Purpose:     purpose,
		TokenHash:   s.keys.UserTokenHash(token),
		CreatedAt:   now,
		ExpiresAt:   expires,
		RequestedIP: client.addr(),
	}); err != nil {
		return "", time.Time{}, fmt.Errorf("registrazione del token: %w", err)
	}
	return token, expires, nil
}

// rehash rigenera l'hash con i parametri correnti, senza far fallire il login se
// non riesce: la password è giusta, e l'utente non deve pagare un problema di
// manutenzione interna.
func (s *Service) rehash(ctx context.Context, user User, password string) {
	hash, err := s.hasher.Hash(ctx, password)
	if err != nil {
		s.log.WarnContext(ctx, "rigenerazione dell'hash fallita",
			slog.String("user_id", user.ID), slog.Any("error", err))
		return
	}
	if err := s.store.UpdatePasswordHash(ctx, user.ID, hash); err != nil {
		s.log.WarnContext(ctx, "aggiornamento dell'hash rigenerato fallito",
			slog.String("user_id", user.ID), slog.Any("error", err))
		return
	}
	s.log.InfoContext(ctx, "hash della password rigenerato con i parametri correnti",
		slog.String("user_id", user.ID))
}

// dispatch mette un messaggio in coda fuori dal percorso della risposta.
//
// Perché non sincrono: il tempo di [Mailer.Send] entra nel tempo di risposta, e
// i messaggi che l'autenticazione manda non sono gli stessi nei due rami che
// devono restare indistinguibili — una conferma d'indirizzo e un avviso «esisti
// già» possono costare diversamente. Spostando l'invio dopo la risposta, quel
// costo esce dalla misura.
func (s *Service) dispatch(ctx context.Context, msg Message) {
	s.run(ctx, func(ctx context.Context) { s.send(ctx, msg) })
}

// send consegna il messaggio al Mailer e ne registra l'esito.
//
// L'errore non risale: SPEC R20.1 stabilisce che il recapito non è osservabile,
// e qui vale di più — un errore riportato al client rimetterebbe in circolo la
// differenza fra un indirizzo con account e uno senza.
func (s *Service) send(ctx context.Context, msg Message) {
	if err := s.mailer.Send(ctx, msg); err != nil {
		s.log.ErrorContext(ctx, "messaggio non messo in coda",
			slog.String("kind", string(msg.Kind)),
			slog.String("user_id", msg.UserID),
			slog.Any("error", err))
	}
}

// run esegue fn dopo che la risposta è stata data.
//
// Il contesto è scollegato da quello della richiesta (`WithoutCancel`) perché la
// richiesta sta per finire e porterebbe via con sé la cancellazione, ma tiene i
// valori — fra cui gli attributi di log — e ha una scadenza propria.
func (s *Service) run(ctx context.Context, fn func(context.Context)) {
	// Add avviene qui, nella goroutine del chiamante: Wait non può quindi
	// osservare un contatore a zero mentre un'attività è già stata decisa.
	s.background.Add(1)
	detached, cancel := context.WithTimeout(context.WithoutCancel(ctx), backgroundTimeout)
	go func() {
		defer s.background.Done()
		defer cancel()
		fn(detached)
	}()
}

// logFailedLogin registra un tentativo fallito.
//
// Non contiene l'indirizzo email: è un dato personale che nel log non serve
// (SPEC §5), e un log pieno di indirizzi tentati sarebbe esso stesso un elenco
// di account da attaccare. La causa serve a distinguere un errore di password da
// una scansione di indirizzi.
func (s *Service) logFailedLogin(ctx context.Context, client Client, reason string) {
	s.log.InfoContext(ctx, "accesso rifiutato",
		slog.String("reason", reason),
		slog.String("client_ip", client.ipKey()))
}

func allow(l *ratelimit.Limiter, key string) error {
	if ok, retryAfter := l.Allow(key); !ok {
		return &RateLimitedError{RetryAfter: retryAfter}
	}
	return nil
}

// emailKey è la chiave di rate limiting per indirizzo.
//
// È l'impronta dell'indirizzo normalizzato e non l'indirizzo: un elenco di
// indirizzi tentati in memoria non è qualcosa che serva conservare. La
// normalizzazione avviene qui e non nel chiamante perché un limite aggirabile
// scrivendo `Mario@Example.COM` invece di `mario@example.com` non è un limite.
func emailKey(email string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(email))))
	return hex.EncodeToString(sum[:16])
}

func durationOr(d, fallback time.Duration) time.Duration {
	if d > 0 {
		return d
	}
	return fallback
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
		LoginPerIP:              pick(l.LoginPerIP, defaults.LoginPerIP),
		LoginPerAccount:         pick(l.LoginPerAccount, defaults.LoginPerAccount),
		RegisterPerIP:           pick(l.RegisterPerIP, defaults.RegisterPerIP),
		PasswordResetPerIP:      pick(l.PasswordResetPerIP, defaults.PasswordResetPerIP),
		PasswordResetPerAccount: pick(l.PasswordResetPerAccount, defaults.PasswordResetPerAccount),
		TokenPerIP:              pick(l.TokenPerIP, defaults.TokenPerIP),
	}
}

// ---------------------------------------------------------------------- email

// maxEmailLength è il limite di lunghezza di un indirizzo: 254 byte è il massimo
// di una casella secondo RFC 5321.
const maxEmailLength = 254

// emailPattern è la stessa espressione del vincolo `users_email_format_check`
// della migrazione 0002.
//
// Deliberatamente permissiva: validare gli indirizzi email con precisione è
// impossibile, e ogni tentativo di farlo rifiuta indirizzi legittimi. L'unica
// verifica che conta è l'invio, ed è quella che il flusso di conferma esegue.
// Qui si scartano solo le forme che non possono essere un indirizzo.
var emailPattern = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)

// NormalizeEmail ripulisce un indirizzo e ne verifica la forma.
//
// Restituisce l'indirizzo con gli spazi ai bordi rimossi, **senza** portarlo in
// minuscolo: la 0002 conserva l'indirizzo come l'utente lo ha scritto e verifica
// l'unicità su `lower(email)`, quindi abbassare qui sarebbe perdere
// informazione. Il confronto in minuscolo è responsabilità dello Store.
func NormalizeEmail(email string) (string, error) {
	trimmed := strings.TrimSpace(email)
	if trimmed == "" || len(trimmed) > maxEmailLength || !emailPattern.MatchString(trimmed) {
		return "", ErrInvalidEmail
	}
	return trimmed, nil
}
