package secrets

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"strings"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/secretbox"
)

// touchInterval è la granularità con cui si aggiorna `last_used_at`.
//
// Senza soglia, ogni occorrenza sarebbe anche una scrittura sui segreti che
// usa — e con la risoluzione al secondo (R22) un solo job produce 86.400
// esecuzioni al giorno. Con cinque minuti «ultimo utilizzo» resta esatto al
// minuto e le scritture diventano una frazione delle letture.
//
// È lo stesso valore, e la stessa scelta, di internal/auth e internal/apikeys.
const touchInterval = 5 * time.Minute

// Options configura il [Service]. Store e Keyring sono obbligatori.
type Options struct {
	Store   Store
	Keyring secretbox.Keyring
	Logger  *slog.Logger

	// Now sostituisce l'orologio. Serve ai test su `last_used_at`.
	Now func() time.Time
}

// Service è la gestione dei segreti del workspace e la loro risoluzione.
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
// funziona a metà: senza chiave non si cifra, e un segreto che non si può
// cifrare non va salvato in chiaro «per intanto».
func NewService(opts Options) (*Service, error) {
	if opts.Store == nil {
		return nil, errors.New("secrets: Store è obbligatorio")
	}
	if !opts.Keyring.Valid() {
		return nil, fmt.Errorf("secrets: keyring non inizializzato (%s)", secretbox.EnvVar)
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

// ------------------------------------------------------------------ creazione

// CreateInput sono i dati di una creazione.
//
// Value è un [Value] e non una `string` di proposito: il valore attraversa le
// rotte, la validazione e il log della creazione, e in nessuno di quei passaggi
// deve poter essere stampato per distrazione.
type CreateInput struct {
	Name        string
	Value       Value
	Description string
}

// Create registra un segreto nuovo.
//
// Il valore in chiaro entra qui, viene cifrato, e **non esce più**: non c'è una
// colonna che lo contenga, non c'è un metodo dello [Store] che lo restituisca, e
// la rotta di elenco serializza [Secret], che non ha il campo. L'unico punto in
// cui torna in chiaro è [Service.Resolve], per il tempo di comporre una
// richiesta HTTP.
//
// A differenza delle chiavi API, il valore **non** viene restituito nemmeno alla
// creazione: lì il segreto lo generavamo noi ed era l'unica occasione di
// consegnarlo; qui ce l'ha già l'utente, che l'ha appena scritto.
func (s *Service) Create(ctx context.Context, userID string, in CreateInput) (Secret, error) {
	name, description, err := validateWrite(in.Name, in.Value, in.Description, true)
	if err != nil {
		return Secret{}, err
	}

	live, err := s.store.CountLiveSecrets(ctx, userID)
	if err != nil {
		return Secret{}, fmt.Errorf("conteggio dei segreti: %w", err)
	}
	if live >= MaxSecretsPerWorkspace {
		return Secret{}, ErrTooManySecrets
	}

	sealed, err := s.seal(userID, name, in.Value)
	if err != nil {
		return Secret{}, err
	}
	sealed.Description = description
	sealed.CreatedAt = s.now()

	secret, err := s.store.CreateSecret(ctx, sealed)
	switch {
	case errors.Is(err, ErrDuplicateName):
		return Secret{}, ErrDuplicateName
	case err != nil:
		return Secret{}, fmt.Errorf("creazione del segreto: %w", err)
	}

	// Nel log ci va il segreto secondo [Secret.LogValue]: identificativo, nome e
	// stato. Il valore non è fra i campi, e non lo è nemmeno il suo testo cifrato.
	s.log.InfoContext(ctx, "segreto del workspace creato", slog.Any("secret", secret))
	return secret, nil
}

// --------------------------------------------------------------- aggiornamento

// UpdateInput sono i dati di un aggiornamento.
//
// Il nome non c'è, e non è una dimenticanza: rinominare un segreto romperebbe in
// silenzio ogni `cron.yaml` che lo riferisce, e la rottura si vedrebbe alla
// prima esecuzione invece che al sync — cioè esattamente il contrario di ciò che
// R43 chiede. Chi vuole un nome diverso ne crea un altro e revoca il vecchio,
// che è la stessa operazione con il push che la accompagna.
type UpdateInput struct {
	Value Value
	// Description nil lascia la nota com'era; una stringa vuota la cancella.
	Description *string
}

// Update sostituisce il valore di un segreto.
//
// Il testo cifrato viene rigenerato per intero, con un nonce nuovo e la chiave
// **attiva**: un aggiornamento è quindi anche una rotazione della singola riga,
// ed è il motivo per cui una rotazione parziale non lascia mai una riga in uno
// stato ambiguo.
func (s *Service) Update(ctx context.Context, userID, secretID string, in UpdateInput) (Secret, error) {
	// Il nome serve alla validazione e al legame crittografico, e a quel punto
	// deve essere quello che sta in tabella: passarlo dal chiamante consentirebbe
	// di risigillare la riga di un segreto con il nome di un altro.
	current, err := s.byID(ctx, userID, secretID)
	if err != nil {
		return Secret{}, err
	}

	description := ""
	if in.Description != nil {
		description = *in.Description
	}
	if _, description, err = validateWrite(current.Name, in.Value, description, in.Description != nil); err != nil {
		return Secret{}, err
	}

	sealed, err := s.seal(userID, current.Name, in.Value)
	if err != nil {
		return Secret{}, err
	}

	var note *string
	if in.Description != nil {
		note = &description
	}

	secret, err := s.store.UpdateSecret(ctx, userID, secretID, sealed, note)
	switch {
	case errors.Is(err, ErrNotFound):
		return Secret{}, ErrSecretNotFound
	case err != nil:
		return Secret{}, fmt.Errorf("aggiornamento del segreto: %w", err)
	}

	s.log.InfoContext(ctx, "segreto del workspace aggiornato", slog.Any("secret", secret))
	return secret, nil
}

// ---------------------------------------------------------------- gestione

// List elenca i segreti del workspace, dal più recente.
//
// Restituisce [Secret], che non ha il campo del valore perché il valore non
// esce. Non c'è nessun parametro, nessuna variante e nessun ruolo — nemmeno
// amministrativo — che lo faccia comparire.
func (s *Service) List(ctx context.Context, userID string, includeRevoked bool) ([]Secret, error) {
	secrets, err := s.store.ListSecrets(ctx, userID, includeRevoked)
	if err != nil {
		return nil, fmt.Errorf("elenco dei segreti: %w", err)
	}
	return secrets, nil
}

// Revoke revoca un segreto.
//
// L'effetto è immediato e definitivo: la revoca **svuota il testo cifrato**
// nella stessa istruzione che scrive la data (vincolo
// `workspace_secrets_revoked_is_empty_check`), quindi il valore smette di
// esistere e non c'è nessun modo di riaverlo. Resta la riga, con il nome e la
// data, perché un'esecuzione che fallisce il giorno dopo va spiegata.
//
// I job che riferiscono quel nome cominciano a fallire, e devono: R43 chiede che
// un riferimento non risolvibile fallisca al sync, ma un segreto revocato *dopo*
// un sync riuscito non ha nessun sync in cui essere annunciato. Il registro delle
// esecuzioni dirà quale nome manca.
func (s *Service) Revoke(ctx context.Context, userID, secretID string) error {
	err := s.store.RevokeSecret(ctx, userID, secretID, s.now())
	switch {
	case errors.Is(err, ErrNotFound):
		return ErrSecretNotFound
	case err != nil:
		return fmt.Errorf("revoca del segreto: %w", err)
	}
	s.log.InfoContext(ctx, "segreto del workspace revocato",
		slog.String("user_id", userID), slog.String("secret_id", secretID))
	return nil
}

// --------------------------------------------------------- validazione al sync

// Available è l'insieme dei nomi dei segreti vivi del workspace.
//
// È ciò che il parser di `cron.yaml` (#422) legge **una volta** per validare
// tutte le voci del file. Non decifra niente: per dire che `${TOKEN}` non esiste
// basta sapere che il nome non c'è.
func (s *Service) Available(ctx context.Context, userID string) (NameSet, error) {
	names, err := s.store.LiveNames(ctx, userID)
	if err != nil {
		return NameSet{}, fmt.Errorf("elenco dei nomi dei segreti: %w", err)
	}
	return NewNameSet(names), nil
}

// Validate verifica che una richiesta sia risolvibile, senza risolverla.
//
// È la forma comoda per chi ha un job solo — le rotte dei job, un controllo
// interattivo in dashboard. Chi ne ha molti usa [Service.Available] e valida in
// memoria.
func (s *Service) Validate(ctx context.Context, userID string, req Request) error {
	available, err := s.Available(ctx, userID)
	if err != nil {
		return err
	}
	return available.Validate(req)
}

// ------------------------------------------------- risoluzione all'esecuzione

// Resolve espande i riferimenti di una richiesta con i valori del workspace.
//
// È il punto in cui i segreti tornano in chiaro, ed è l'unico. Il risultato è un
// [Resolved], che i valori li tiene in campi privati e che stampato mostra la
// richiesta **non** risolta.
//
// # Quando fallisce
//
// Con un [*ValidationError] se un riferimento non è risolvibile: nome
// sconosciuto, segreto revocato fra il sync e l'esecuzione. Non è la via
// normale — R43 vuole che questo errore l'utente lo veda al `git push` — ma la
// via normale non copre ciò che cambia *dopo* il sync, e un'esecuzione che parte
// con un `${TOKEN}` letterale dentro alla testata `Authorization` sarebbe peggio
// di un'esecuzione fallita: manderebbe al bersaglio una credenziale sbagliata,
// che è il modo di farsi bloccare l'account.
//
// # Il costo
//
// Una lettura indicizzata per esecuzione, sui soli nomi riferiti, e nessuna
// cache. È la stessa scelta della revoca immediata delle chiavi API, per lo
// stesso motivo: con una cache, «revocato» significherebbe revocato fra cinque
// minuti. Una richiesta che non riferisce nessun segreto non legge niente.
func (s *Service) Resolve(ctx context.Context, userID string, req Request) (Resolved, error) {
	refs, err := References(req)
	if err != nil {
		return Resolved{}, err
	}
	names := Names(refs)
	if len(names) == 0 {
		// Nessun riferimento: niente da leggere e niente da decifrare. Il
		// [Resolved] c'è comunque, con un redattore vuoto, perché chi esegue deve
		// poter produrre l'estratto della risposta allo stesso modo in tutti i casi.
		return Resolved{
			template: req,
			url:      req.URL,
			headers:  maps.Clone(req.Headers),
			body:     req.Body,
		}, nil
	}

	sealed, err := s.store.LiveByNames(ctx, userID, names)
	if err != nil {
		return Resolved{}, fmt.Errorf("lettura dei segreti: %w", err)
	}

	values := make(map[string]Value, len(sealed))
	ids := make([]string, 0, len(sealed))
	for _, row := range sealed {
		plaintext, err := s.keys.OpenPlaintext(secretbox.Box{
			Ciphertext: row.Ciphertext,
			Nonce:      row.Nonce,
			KeyVersion: row.KeyVersion,
		}, binding(row.UserID, row.Name))
		if err != nil {
			// Il segreto c'è ma non si apre: chiave rimossa dall'ambiente, riga
			// manomessa, testo cifrato copiato da un'altra riga. Nel log ci va il
			// nome, mai il materiale cifrato.
			s.log.ErrorContext(ctx, "segreto non decifrabile",
				slog.Any("secret", row.Secret), slog.Any("error", err))
			return Resolved{}, fmt.Errorf("decifratura del segreto %q: %w", row.Name, err)
		}
		values[row.Name] = plaintext
		if row.LastUsedAt == nil || s.now().Sub(*row.LastUsedAt) >= touchInterval {
			ids = append(ids, row.ID)
		}
	}

	// I nomi che la lettura non ha restituito sono riferimenti non risolvibili.
	// Passano dallo stesso [NameSet.Validate] della validazione al sync, così che
	// il messaggio sia identico nei due momenti.
	if len(values) != len(names) {
		err := NewNameSet(keysOf(values)).Validate(req)
		if err == nil {
			// Lo Store ha restituito nomi che nessuno aveva chiesto: è un difetto
			// dell'implementazione, e va detto invece di far partire una richiesta
			// composta con valori che non sappiamo da dove vengano.
			err = fmt.Errorf("secrets: lo Store ha restituito %d segreti per %d nomi richiesti",
				len(values), len(names))
		}
		return Resolved{}, err
	}

	resolved := Resolved{
		template: req,
		url:      expandWith(req.URL, values),
		headers:  make(map[string]string, len(req.Headers)),
		body:     expandWith(req.Body, values),
		redactor: newRedactor(values),
	}
	for key, value := range req.Headers {
		resolved.headers[key] = expandWith(value, values)
	}

	s.touch(ctx, ids)
	return resolved, nil
}

// touch aggiorna `last_used_at` dei segreti appena risolti.
//
// Un errore non ferma l'esecuzione: non registrare l'uso è un difetto della
// traccia, non un motivo per non fare partire una richiesta che è già stata
// autorizzata e composta.
func (s *Service) touch(ctx context.Context, ids []string) {
	if len(ids) == 0 {
		return
	}
	if err := s.store.TouchSecrets(ctx, ids, s.now()); err != nil {
		s.log.WarnContext(ctx, "aggiornamento di last_used_at dei segreti fallito",
			slog.Int("secrets", len(ids)), slog.Any("error", err))
	}
}

// ------------------------------------------------------------------ interni

// binding è il dato che lega un testo cifrato alla riga che lo contiene.
//
// Entra nell'autenticazione di AES-GCM (vedi internal/secretbox): un testo
// cifrato spostato su un'altra riga — altro utente, altro nome — non si apre. Il
// separatore è un byte che nei due campi non può comparire, così che
// `("ab", "c")` e `("a", "bc")` non producano lo stesso legame.
func binding(userID, name string) []byte {
	return []byte("postqron/v1/workspace-secret\x00" + userID + "\x00" + name)
}

// seal cifra un valore per una riga.
func (s *Service) seal(userID, name string, value Value) (Sealed, error) {
	box, err := s.keys.SealPlaintext(value, binding(userID, name))
	if err != nil {
		return Sealed{}, fmt.Errorf("cifratura del segreto: %w", err)
	}
	return Sealed{
		Secret: Secret{
			UserID:     userID,
			Name:       name,
			KeyVersion: box.KeyVersion,
		},
		Ciphertext: box.Ciphertext,
		Nonce:      box.Nonce,
	}, nil
}

// byID legge un segreto vivo del workspace.
func (s *Service) byID(ctx context.Context, userID, secretID string) (Secret, error) {
	secret, err := s.store.SecretByID(ctx, userID, secretID)
	switch {
	case errors.Is(err, ErrNotFound):
		return Secret{}, ErrSecretNotFound
	case err != nil:
		return Secret{}, fmt.Errorf("lettura del segreto: %w", err)
	}
	return secret, nil
}

// expandWith sostituisce i riferimenti noti. Gli errori di sintassi sono già
// stati raccolti da [References] prima di arrivare qui.
func expandWith(text string, values map[string]Value) string {
	return expand(text, "", &ValidationError{}, func(name string) (string, bool) {
		value, ok := values[name]
		return value.Reveal(), ok
	})
}

func keysOf(values map[string]Value) []string {
	out := make([]string, 0, len(values))
	for name := range values {
		out = append(out, name)
	}
	return out
}

// validateWrite normalizza e verifica nome, valore e nota.
//
// `checkDescription` distingue la creazione, dove la nota c'è sempre, da un
// aggiornamento che non la tocca.
//
// I motivi di rifiuto si raccolgono tutti invece di fermarsi al primo: chi
// compila un form vuole sapere in un giro tutto quello che c'è da correggere.
func validateWrite(name string, value Value, description string, checkDescription bool) (string, string, error) {
	invalid := &ValidationError{}

	name = strings.TrimSpace(name)
	switch {
	case name == "":
		invalid.add("name", "required",
			"Dai un nome al segreto: è quello che scriverai come `${NOME}` in `cron.yaml`.")
	case len(name) > MaxNameLength:
		invalid.add("name", "too_long",
			fmt.Sprintf("Il nome non può superare %d caratteri.", MaxNameLength))
	case !ValidName(name):
		invalid.add("name", "invalid_format", invalidNameMessage(name))
	}

	// Il valore **non** viene ripulito dagli spazi ai bordi: uno spazio finale in
	// un token è indistinguibile da uno battuto per sbaglio, ma toglierlo
	// significherebbe che il segreto salvato non è quello incollato — e il
	// fallimento che ne segue è fra i più difficili da capire che esistano.
	switch {
	case value.Empty():
		invalid.add("value", "required", "Il valore del segreto non può essere vuoto.")
	case value.Len() < MinValueLength:
		invalid.add("value", "too_short", fmt.Sprintf(
			"Il valore deve essere di almeno %d caratteri. Sotto quella soglia non è possibile "+
				"nasconderlo con sicurezza negli estratti delle risposte, che l'utente legge.",
			MinValueLength))
	case value.Len() > MaxValueLength:
		invalid.add("value", "too_long",
			fmt.Sprintf("Il valore non può superare %d caratteri.", MaxValueLength))
	}

	if checkDescription {
		description = strings.TrimSpace(description)
		if len(description) > MaxDescriptionLength {
			invalid.add("description", "too_long",
				fmt.Sprintf("La nota non può superare %d caratteri.", MaxDescriptionLength))
		}
	}

	return name, description, invalid.orNil()
}

// invalidNameMessage spiega perché un nome non è ammesso, indicando la
// correzione quando è ovvia.
func invalidNameMessage(name string) string {
	if upper := strings.ToUpper(name); upper != name && ValidName(upper) {
		return fmt.Sprintf("I nomi dei segreti sono in maiuscolo: usa %q invece di %q.", upper, name)
	}
	return "Il nome comincia con una lettera maiuscola e prosegue con maiuscole, cifre e underscore " +
		"(per esempio `DIGEST_TOKEN`)."
}
