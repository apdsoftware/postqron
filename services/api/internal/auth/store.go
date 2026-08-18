package auth

import (
	"context"
	"errors"
	"net/netip"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/legal"
)

// TokenPurpose è lo scopo di un token monouso; corrisponde al tipo
// `user_token_purpose` della migrazione 0009.
type TokenPurpose string

const (
	// PurposeEmailVerification conferma che l'indirizzo è raggiungibile.
	PurposeEmailVerification TokenPurpose = "email_verification"
	// PurposePasswordReset autorizza a impostare una password nuova.
	PurposePasswordReset TokenPurpose = "password_reset"
)

// User è la parte di `users` che serve all'autenticazione.
//
// Non è l'intero record: il fuso, il piano e il resto del profilo appartengono
// ad altre issue, e questo package non deve diventare il punto da cui passano.
type User struct {
	ID              string
	Email           string
	FullName        string
	Role            string
	Timezone        string
	PasswordHash    string
	EmailVerifiedAt *time.Time
	SuspendedAt     *time.Time
	LastLoginAt     *time.Time
	CreatedAt       time.Time
}

// Suspended indica se l'account è sospeso.
func (u User) Suspended() bool { return u.SuspendedAt != nil }

// EmailVerified indica se l'indirizzo è stato confermato.
func (u User) EmailVerified() bool { return u.EmailVerifiedAt != nil }

// Session è una sessione di login.
//
// Non contiene il token: il valore in chiaro esiste una volta sola, al momento
// in cui viene consegnato al client.
type Session struct {
	ID         string
	UserID     string
	TokenHash  string
	CreatedAt  time.Time
	LastUsedAt time.Time
	ExpiresAt  time.Time
	RevokedAt  *time.Time
	IPAddress  *netip.Addr
	UserAgent  string
}

// Active indica se la sessione è utilizzabile a un dato istante, considerando
// revoca, scadenza assoluta e inattività.
func (s Session) Active(now time.Time, idleTTL time.Duration) bool {
	if s.RevokedAt != nil {
		return false
	}
	if !now.Before(s.ExpiresAt) {
		return false
	}
	return idleTTL <= 0 || now.Sub(s.LastUsedAt) < idleTTL
}

// NewUser è un account da creare, con la prova di ciò che chi lo crea ha
// accettato.
//
// È una struttura e non quattro parametri perché i consensi non sono un
// argomento in più: sono la ragione per cui questa operazione è indivisibile.
type NewUser struct {
	Email        string
	PasswordHash string
	FullName     string

	// Consents sono i consensi ai documenti legali in vigore (R46), da scrivere
	// **nella stessa transazione dell'account**.
	//
	// Non è un dettaglio di efficienza. Un account senza la prova di ciò che ha
	// accettato è uno stato che non deve poter esistere: se la scrittura fosse
	// una seconda chiamata, un errore fra le due lascerebbe un utente iscritto
	// di cui non sappiamo dire cosa aveva davanti — e non c'è un modo di
	// ricostruirlo dopo, perché la domanda è cosa vedeva *in quel momento*.
	//
	// Vuoto è ammesso, e serve ai test che non hanno un registro: lo Store
	// scrive l'account e basta.
	Consents []legal.Consent
}

// UserToken è un token monouso già ridotto alla sua impronta.
type UserToken struct {
	ID          string
	UserID      string
	Purpose     TokenPurpose
	TokenHash   string
	CreatedAt   time.Time
	ExpiresAt   time.Time
	ConsumedAt  *time.Time
	RequestedIP *netip.Addr
}

// Errori che lo Store restituisce e che il Service distingue.
var (
	// ErrNotFound indica che la riga cercata non esiste. Chi lo riceve non deve
	// farlo arrivare al client: la differenza fra «non c'è» e «c'è» è
	// esattamente ciò che un attaccante vuole misurare.
	ErrNotFound = errors.New("non trovato")
	// ErrEmailTaken indica che l'indirizzo è già usato da un account vivo.
	ErrEmailTaken = errors.New("indirizzo già registrato")
)

// Store è la persistenza di cui l'autenticazione ha bisogno.
//
// È un'interfaccia e non il pool di pgx per due ragioni: i test del Service —
// compresi quelli sull'enumerazione e sul rate limiting — girano senza database
// e restano nella CI di una macchina senza `make db-up`, e l'implementazione
// PostgreSQL vive in un package separato (internal/authpg) dove le query si
// leggono tutte insieme.
//
// Gli identificativi sono stringhe (uuid in forma testuale) perché il tipo uuid
// di pgx non deve attraversare questo confine.
type Store interface {
	// UserByEmail cerca un account vivo per indirizzo, confrontando in forma
	// minuscola come fa l'indice unico della 0002. Un account cancellato
	// logicamente è [ErrNotFound].
	UserByEmail(ctx context.Context, email string) (User, error)
	// UserByID cerca un account vivo per identificativo.
	UserByID(ctx context.Context, id string) (User, error)
	// CreateUser crea un account. Se l'indirizzo è già preso restituisce
	// [ErrEmailTaken] senza modificare nulla.
	//
	// L'account e i suoi consensi nascono **nella stessa transazione**: vedi
	// [NewUser.Consents].
	CreateUser(ctx context.Context, in NewUser) (User, error)
	// UpdatePasswordHash sostituisce l'hash della password.
	UpdatePasswordHash(ctx context.Context, userID, passwordHash string) error
	// TouchLastLogin registra il momento dell'ultimo accesso riuscito.
	TouchLastLogin(ctx context.Context, userID string, at time.Time) error
	// MarkEmailVerified segna l'indirizzo come confermato, se non lo era già.
	MarkEmailVerified(ctx context.Context, userID string, at time.Time) error

	// CreateSession registra una sessione nuova.
	CreateSession(ctx context.Context, s Session) (Session, error)
	// SessionByTokenHash cerca una sessione per impronta del token.
	SessionByTokenHash(ctx context.Context, tokenHash string) (Session, error)
	// TouchSession aggiorna `last_used_at`.
	TouchSession(ctx context.Context, id string, at time.Time) error
	// ListSessions elenca le sessioni vive di un utente, dalla più recente.
	ListSessions(ctx context.Context, userID string, now time.Time) ([]Session, error)
	// RevokeSession revoca una sessione dell'utente indicato. L'ambito
	// sull'utente è parte del contratto: senza, l'identificativo di una sessione
	// altrui basterebbe a chiuderla. Restituisce [ErrNotFound] se la sessione
	// non esiste, non è dell'utente o era già revocata.
	RevokeSession(ctx context.Context, userID, sessionID string, at time.Time) error
	// RevokeSessionByTokenHash revoca la sessione corrente (logout).
	RevokeSessionByTokenHash(ctx context.Context, tokenHash string, at time.Time) error
	// RevokeUserSessions revoca tutte le sessioni vive di un utente; se
	// `exceptID` non è vuoto, quella sessione viene risparmiata. Restituisce
	// quante ne ha revocate.
	RevokeUserSessions(ctx context.Context, userID, exceptID string, at time.Time) (int, error)

	// CreateUserToken registra un token monouso.
	CreateUserToken(ctx context.Context, t UserToken) (UserToken, error)
	// ConsumeUserToken segna come consumato il token indicato e ne restituisce
	// il contenuto, in un'unica operazione atomica: due richieste concorrenti
	// con lo stesso token devono produrre un vincitore e un [ErrNotFound], non
	// due reimpostazioni. Un token scaduto, già consumato o di scopo diverso è
	// [ErrNotFound].
	ConsumeUserToken(ctx context.Context, tokenHash string, purpose TokenPurpose, now time.Time) (UserToken, error)
	// RevokeUserTokens consuma senza usarli tutti i token pendenti di un utente
	// per un dato scopo. Serve quando se ne emette uno nuovo — il precedente
	// non deve restare valido — e dopo una reimpostazione riuscita.
	RevokeUserTokens(ctx context.Context, userID string, purpose TokenPurpose, at time.Time) (int, error)
}
