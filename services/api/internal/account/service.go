package account

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

// Confirmer verifica che chi chiede conosca la password dell'account.
//
// La implementa [auth.Service.ConfirmPassword]. È un'interfaccia e non il
// servizio concreto perché internal/auth non deve entrare qui: la cancellazione
// ha bisogno di **una** risposta da lui, e chiedergliela per interfaccia
// permette di provare questo package senza costruire un servizio di
// autenticazione intero.
type Confirmer interface {
	ConfirmPassword(ctx context.Context, userID, password string) error
}

// Options configura il [Service]. Store e Confirm sono obbligatori.
type Options struct {
	Store   Store
	Confirm Confirmer
	Logger  *slog.Logger

	// Grace è il periodo di ripensamento. Zero significa [DefaultGrace], che è
	// il valore promesso dalla privacy policy §5.
	//
	// Un valore negativo è rifiutato dalla costruzione: significherebbe una
	// scadenza già passata al momento della richiesta, cioè una cancellazione
	// immediata mascherata da cancellazione con grazia. Se un giorno dovesse
	// servire una purga senza attesa, il modo di chiederla è zero, che si legge
	// per quello che è.
	Grace time.Duration

	// Now è l'orologio, iniettabile per i test.
	Now func() time.Time
}

// Service applica R45. Va costruito con [New].
type Service struct {
	store   Store
	confirm Confirmer
	log     *slog.Logger
	grace   time.Duration
	now     func() time.Time
}

// New costruisce il servizio.
func New(opts Options) (*Service, error) {
	if opts.Store == nil {
		return nil, errors.New("account: lo store è obbligatorio")
	}
	if opts.Confirm == nil {
		// Senza conferma la cancellazione sarebbe raggiungibile da una sessione
		// sola, ed è irreversibile: costruire il servizio senza è un errore di
		// cablaggio, non una degradazione accettabile.
		return nil, errors.New("account: la conferma della password è obbligatoria (R45)")
	}
	if opts.Grace < 0 {
		return nil, errors.New("account: il periodo di grazia non può essere negativo")
	}

	s := &Service{
		store:   opts.Store,
		confirm: opts.Confirm,
		log:     opts.Logger,
		grace:   opts.Grace,
		now:     opts.Now,
	}
	if s.log == nil {
		s.log = slog.Default()
	}
	if s.grace == 0 {
		s.grace = DefaultGrace
	}
	if s.now == nil {
		s.now = time.Now
	}
	return s, nil
}

// Grace è il periodo di ripensamento in vigore. Serve alla dashboard, che deve
// poterlo dire all'utente prima che chieda, e non dopo.
func (s *Service) Grace() time.Duration { return s.grace }

// GraceFromEnv legge [GraceEnvVar]. Vuota significa [DefaultGrace].
//
// Il formato è quello di [time.ParseDuration] (`720h`), che non conosce i
// giorni: è scomodo per un valore che si esprime in giorni, ed è lo stesso
// formato di `POSTQRON_SHUTDOWN_TIMEOUT` e di ogni altra durata del servizio.
// Una sintassi propria per questa sola variabile sarebbe una cosa in più da
// sapere per chi configura.
func GraceFromEnv(getenv func(string) string) (time.Duration, error) {
	raw := getenv(GraceEnvVar)
	if raw == "" {
		return DefaultGrace, nil
	}
	grace, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("%s non è una durata valida: %w", GraceEnvVar, err)
	}
	if grace < 0 {
		return 0, fmt.Errorf("%s non può essere negativa", GraceEnvVar)
	}
	return grace, nil
}

// RequestInput è ciò che l'utente manda per chiedere la cancellazione.
type RequestInput struct {
	// Password è la conferma di R45. Vedi [Service.RequestDeletion].
	Password string

	// SubscriptionAcknowledged è la presa d'atto che chiudere l'account **non**
	// annulla l'abbonamento presso Paddle. Vedi [Service.RequestDeletion].
	SubscriptionAcknowledged bool
}

// SubscriptionActiveError è [ErrSubscriptionActive] con dentro la sottoscrizione
// che l'ha causato.
//
// Il livello HTTP ne ha bisogno per dire *quale* piano resta vivo: un rifiuto
// che dice solo «hai un abbonamento» costringe la dashboard a fare una seconda
// richiesta per costruire il messaggio, e nel frattempo mostra un errore che non
// spiega niente.
type SubscriptionActiveError struct {
	Subscription Subscription
}

func (e *SubscriptionActiveError) Error() string {
	return fmt.Sprintf("account: sottoscrizione %q attiva sul piano %s", e.Subscription.PaddleSubscriptionID, e.Subscription.PlanCode)
}

func (e *SubscriptionActiveError) Unwrap() error { return ErrSubscriptionActive }

// Status legge lo stato della cancellazione.
func (s *Service) Status(ctx context.Context, userID string) (Status, error) {
	status, err := s.store.Status(ctx, userID)
	if err != nil {
		return Status{}, err
	}
	return status, nil
}

// RequestDeletion apre la finestra di ripensamento, ferma le esecuzioni e revoca
// le chiavi (R45).
//
// # Due conferme, e perché sono due
//
// **La password.** La cancellazione è irreversibile e una sessione rubata da un
// portatile lasciato aperto non deve bastare a distruggere il lavoro di
// qualcuno. È la stessa protezione che [auth.Service.ChangePassword] applica al
// cambio di password, e per lo stesso motivo.
//
// **La presa d'atto sull'abbonamento**, e solo quando ce n'è uno a pagamento
// vivo. Qui la ragione non è di sicurezza ma di onestà, ed è la conseguenza di
// una cosa che il prodotto non può fare:
//
//   - **Paddle è Merchant of Record** (SPEC §2, Termini §1): il contratto di
//     vendita è fra l'utente e Paddle, non fra l'utente e noi. L'abbonamento non
//     è nostro da annullare, e infatti da qui non parte nessuna chiamata verso
//     Paddle — `PADDLE_API_KEY` esiste in configurazione e non la usa nessuno.
//   - I Termini §4.3 dicono che **non c'è rimborso pro rata**. Un utente che
//     chiude l'account e continua a essere fatturato per mesi ha un problema
//     vero, e noi non abbiamo lo strumento per rimediare.
//
// Da qui il rifiuto alla prima richiesta, con dentro il piano e
// l'identificativo della sottoscrizione: la dashboard può dire cosa resterà
// vivo e chiedere di confermarlo. **Non è un divieto** — i Termini §7 dicono
// «You may close your account at any time» e la seconda richiesta passa — è la
// differenza fra lasciar decidere e lasciar succedere.
//
// # Perché la richiesta non è idempotente
//
// Ripeterla su un account già in cancellazione restituisce
// [ErrAlreadyRequested] invece di spostare la scadenza: rimandare una
// cancellazione che l'utente aveva già chiesto è una decisione, e non deve
// prenderla il fatto che qualcuno abbia premuto due volte.
func (s *Service) RequestDeletion(ctx context.Context, userID string, in RequestInput) (Receipt, error) {
	if err := s.confirm.ConfirmPassword(ctx, userID, in.Password); err != nil {
		return Receipt{}, err
	}

	status, err := s.store.Status(ctx, userID)
	if err != nil {
		return Receipt{}, err
	}
	if status.Requested {
		return Receipt{}, ErrAlreadyRequested
	}
	if status.Subscription.Paid && !in.SubscriptionAcknowledged {
		return Receipt{}, &SubscriptionActiveError{Subscription: status.Subscription}
	}

	now := s.now()
	receipt, err := s.store.RequestDeletion(ctx, userID, now, now.Add(s.grace))
	if err != nil {
		return Receipt{}, err
	}

	// Va lasciato scritto, e con i numeri: «abbiamo interrotto le esecuzioni e
	// revocato le chiavi» è una frase di un documento legale, e un log che la
	// ripete senza dire quante cose ha toccato non la rende verificabile.
	s.log.InfoContext(ctx, "cancellazione dell'account richiesta (R45)",
		slog.String("user_id", userID),
		slog.Time("purga_dopo", receipt.PurgeAfter),
		slog.Int("job_fermati", receipt.JobsStopped),
		slog.Int("chiavi_api_revocate", receipt.KeysRevoked),
		slog.Int("segreti_revocati", receipt.SecretsRevoked),
		slog.Int("chiavi_ai_revocate", receipt.AIKeysRevoked),
		slog.Int("sessioni_chiuse", receipt.SessionsRevoked),
		slog.Bool("abbonamento_a_pagamento_vivo", status.Subscription.Paid))

	return receipt, nil
}

// CancelDeletion annulla la richiesta durante la grazia.
//
// È il «during which you can change your mind» della privacy policy §5, e ciò
// che rimette in piedi è **meno** di ciò che la richiesta aveva fermato: i job
// ripartono, le chiavi no. La revoca ha svuotato il materiale cifrato — la 0012
// e la 0016 lo impongono con un vincolo — quindi non c'è niente da restituire.
// È coerente con il documento, che promette la revoca come immediata e non come
// sospensione.
//
// **Non chiede la password.** Chi annulla sta togliendo un pericolo, non
// creandone uno: la conferma protegge dall'azione distruttiva, e questa è
// l'azione opposta.
func (s *Service) CancelDeletion(ctx context.Context, userID string) (Restored, error) {
	restored, err := s.store.CancelDeletion(ctx, userID)
	if err != nil {
		return Restored{}, err
	}

	s.log.InfoContext(ctx, "cancellazione dell'account annullata (R45)",
		slog.String("user_id", userID),
		slog.Int("job_riaccesi", restored.JobsResumed))

	return restored, nil
}
