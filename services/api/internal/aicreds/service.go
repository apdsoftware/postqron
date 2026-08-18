package aicreds

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/secretbox"
)

// touchInterval è la granularità con cui si aggiorna `last_used_at`.
//
// È più larga di quella dei segreti del workspace, e per una ragione: là la
// risoluzione gira a ogni occorrenza di ogni job, qui una chiave AI viene letta
// quando l'utente chiede l'analisi di un log fallito. Il valore resta comunque
// una soglia e non un contatore, perché il debugging automatico di un job che
// fallisce ogni minuto (R30) leggerebbe la chiave altrettanto spesso.
const touchInterval = 5 * time.Minute

// Options configura il [Service]. Store e Keyring sono obbligatori.
type Options struct {
	Store   Store
	Keyring secretbox.Keyring
	Logger  *slog.Logger

	// Now sostituisce l'orologio. Serve ai test su `last_used_at`.
	Now func() time.Time
}

// Service è la gestione delle chiavi AI degli utenti.
// Va costruito con [NewService].
type Service struct {
	store Store
	keys  secretbox.Keyring
	log   *slog.Logger
	now   func() time.Time
}

// NewService costruisce il servizio.
//
// Un keyring non inizializzato è un errore d'avvio e non un servizio che
// funziona a metà: senza chiave non si cifra, e una chiave AI che non si può
// cifrare non va salvata in chiaro «per intanto».
func NewService(opts Options) (*Service, error) {
	if opts.Store == nil {
		return nil, errors.New("aicreds: Store è obbligatorio")
	}
	if !opts.Keyring.Valid() {
		return nil, fmt.Errorf("aicreds: keyring non inizializzato (%s)", secretbox.EnvVar)
	}

	s := &Service{store: opts.Store, keys: opts.Keyring, log: opts.Logger, now: opts.Now}
	if s.log == nil {
		s.log = slog.Default()
	}
	if s.now == nil {
		s.now = time.Now
	}
	return s, nil
}

// ---------------------------------------------------------------- scrittura

// SaveInput sono i dati di un inserimento.
//
// Key è un [secretbox.Plaintext] e non una `string` di proposito: la chiave
// attraversa le rotte, la validazione e il log della scrittura, e in nessuno di
// quei passaggi deve poter essere stampata per distrazione.
type SaveInput struct {
	Provider string
	Key      secretbox.Plaintext
	Label    string
}

// Save registra la chiave di un provider, sostituendo quella viva se c'è.
//
// La chiave in chiaro entra qui, viene cifrata, e **non esce più** da questa
// strada: non c'è una colonna che la contenga, non c'è un metodo dello [Store]
// che la restituisca, e la rotta di elenco serializza [Credential], che non ha
// il campo. L'unico punto in cui torna in chiaro è [Service.Reveal], che le
// rotte non chiamano.
//
// Non c'è una `Create` distinta da un `Update`, e non è una semplificazione:
// l'unicità della 0016 ammette una chiave viva per provider, quindi un secondo
// inserimento *è* la sostituzione. Rifiutarlo con un conflitto costringerebbe a
// revocare prima di incollare, cioè a passare da uno stato in cui il debugging
// AI non funziona per fare una cosa che voleva solo aggiornarlo.
//
// Il materiale cifrato viene rigenerato per intero, con un nonce nuovo e la
// chiave **attiva** del keyring: un aggiornamento è quindi anche una rotazione
// della singola riga.
func (s *Service) Save(ctx context.Context, userID string, in SaveInput) (Credential, error) {
	provider, label, err := validateWrite(in.Provider, in.Key, in.Label)
	if err != nil {
		return Credential{}, err
	}

	box, err := s.keys.SealPlaintext(in.Key, binding(userID, provider))
	if err != nil {
		// L'errore del cifrario non porta con sé il testo in chiaro, ma non lo
		// diamo per scontato: qui si avvolge con un messaggio nostro, e ciò che
		// arriva al chiamante dice cosa non è riuscito e su quale riga.
		return Credential{}, fmt.Errorf("cifratura della chiave AI per %s: %w", provider, err)
	}

	credential, err := s.store.UpsertCredential(ctx, Sealed{
		Credential: Credential{
			UserID:     userID,
			Provider:   provider,
			Label:      label,
			KeyVersion: box.KeyVersion,
			CreatedAt:  s.now(),
		},
		Ciphertext: box.Ciphertext,
		Nonce:      box.Nonce,
	})
	if err != nil {
		return Credential{}, fmt.Errorf("scrittura della chiave AI: %w", err)
	}

	// Nel log ci va la credenziale secondo [Credential.LogValue]: identificativo,
	// utente, provider e stato. La chiave non è fra i campi, e non lo è nemmeno il
	// suo testo cifrato.
	s.log.InfoContext(ctx, "chiave AI salvata", slog.Any("credential", credential))
	return credential, nil
}

// ------------------------------------------------------------------ gestione

// List elenca le chiavi AI dell'utente, dalla più recente.
//
// Restituisce [Credential], che non ha il campo della chiave né un frammento di
// essa. Non c'è nessun parametro, nessuna variante e nessun ruolo — nemmeno
// amministrativo — che la faccia comparire.
func (s *Service) List(ctx context.Context, userID string, includeRevoked bool) ([]Credential, error) {
	credentials, err := s.store.ListCredentials(ctx, userID, includeRevoked)
	if err != nil {
		return nil, fmt.Errorf("elenco delle chiavi AI: %w", err)
	}
	return credentials, nil
}

// Revoke revoca una chiave AI.
//
// L'effetto è immediato e definitivo: la revoca **svuota il materiale cifrato**
// nella stessa istruzione che scrive la data (vincolo
// `ai_credentials_revoked_is_empty_check`), quindi la chiave smette di esistere
// da noi e non c'è nessun modo di riaverla — nemmeno con `ENCRYPTION_KEY` alla
// mano. Resta la riga, con il provider e la data, perché un'analisi dei log che
// smette di funzionare il giorno dopo va spiegata.
//
// Il provider torna libero: l'unicità della 0016 vale fra le sole chiavi vive,
// quindi l'utente può incollarne subito un'altra.
func (s *Service) Revoke(ctx context.Context, userID, credentialID string) error {
	err := s.store.RevokeCredential(ctx, userID, credentialID, s.now())
	switch {
	case errors.Is(err, ErrNotFound):
		return ErrCredentialNotFound
	case err != nil:
		return fmt.Errorf("revoca della chiave AI: %w", err)
	}
	s.log.InfoContext(ctx, "chiave AI revocata",
		slog.String("user_id", userID), slog.String("credential_id", credentialID))
	return nil
}

// ------------------------------------------------------------ l'unica lettura

// Reveal restituisce la chiave in chiaro di un provider.
//
// **È l'unico punto del prodotto in cui una chiave AI torna leggibile**, e non
// sta dietro nessuna rotta: lo chiama il backend, dal lato server, per comporre
// la chiamata al fornitore che analizza i log di un'esecuzione fallita (R30,
// #437). Il metodo si chiama come si chiama per la stessa ragione di
// [secretbox.Plaintext.Reveal]: se compare in una riga che costruisce una
// risposta HTTP, quella riga è un difetto e si vede in revisione.
//
// # Che cosa non fa
//
// Non logga la chiave, e non la mette in nessun errore. È scritto qui perché è
// il posto in cui sarebbe comodo farlo: quando la decifratura fallisce si vuole
// sapere *cosa* non si è aperto, e la risposta comoda sarebbe stampare il
// materiale. Nel log va la credenziale secondo [Credential.LogValue] — chi, che
// provider, che versione di chiave — che è ciò che serve davvero a distinguere
// «`ENCRYPTION_KEY` è cambiata» da «la riga è stata manomessa».
//
// # Quando fallisce
//
// Con [ErrNoLiveKey] se l'utente non ha una chiave viva per quel provider: è il
// caso normale di chi non ha ancora configurato il BYOK, e chi chiama deve
// distinguerlo da un guasto per dire «configura una chiave» invece di «riprova».
func (s *Service) Reveal(ctx context.Context, userID string, provider Provider) (secretbox.Plaintext, error) {
	if !provider.Valid() {
		return "", fmt.Errorf("aicreds: provider sconosciuto %q", provider)
	}

	sealed, err := s.store.LiveByProvider(ctx, userID, provider)
	switch {
	case errors.Is(err, ErrNotFound):
		return "", ErrNoLiveKey
	case err != nil:
		return "", fmt.Errorf("lettura della chiave AI: %w", err)
	}

	key, err := s.keys.OpenPlaintext(secretbox.Box{
		Ciphertext: sealed.Ciphertext,
		Nonce:      sealed.Nonce,
		KeyVersion: sealed.KeyVersion,
	}, binding(sealed.UserID, sealed.Provider))
	if err != nil {
		// La chiave c'è ma non si apre: `ENCRYPTION_KEY` rimossa dall'ambiente,
		// riga manomessa, materiale copiato da un'altra riga. Nel log ci va la
		// credenziale, mai il materiale.
		s.log.ErrorContext(ctx, "chiave AI non decifrabile",
			slog.Any("credential", sealed.Credential), slog.Any("error", err))
		return "", fmt.Errorf("decifratura della chiave AI di %s: %w", provider, err)
	}

	s.touch(ctx, sealed)
	return key, nil
}

// touch aggiorna `last_used_at` della chiave appena letta.
//
// Un errore non ferma niente: non registrare l'uso è un difetto della traccia,
// non un motivo per non fare partire un'analisi che è già stata autorizzata.
func (s *Service) touch(ctx context.Context, sealed Sealed) {
	if sealed.LastUsedAt != nil && s.now().Sub(*sealed.LastUsedAt) < touchInterval {
		return
	}
	if err := s.store.TouchCredential(ctx, sealed.ID, s.now()); err != nil {
		s.log.WarnContext(ctx, "aggiornamento di last_used_at della chiave AI fallito",
			slog.Any("credential", sealed.Credential), slog.Any("error", err))
	}
}
