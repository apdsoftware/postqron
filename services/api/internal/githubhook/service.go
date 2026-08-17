package githubhook

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// SecretEnvVar è la variabile che porta il segreto del webhook della GitHub App
// (CREDENTIALS §5). Sta qui e non in internal/config perché quel package è
// condiviso fra le issue in corso: il valore lo legge chi lo usa.
const SecretEnvVar = "GITHUB_APP_WEBHOOK_SECRET"

// minSecretLen è la lunghezza sotto la quale un segreto non è un segreto.
//
// GitHub suggerisce di generarlo casuale e lungo; sedici caratteri sono già
// pochi, e il controllo serve a intercettare il caso vero — una variabile
// riempita con un valore di comodo durante una prova e mai sostituita.
const minSecretLen = 16

// SecretFromEnv legge il segreto del webhook dall'ambiente.
func SecretFromEnv(getenv func(string) string) string {
	return strings.TrimSpace(getenv(SecretEnvVar))
}

// Options configura il [Service]. Secret e Store sono obbligatori.
type Options struct {
	// Secret è il segreto del webhook della GitHub App.
	Secret string

	// Store registra le consegne già ricevute. Senza, non c'è idempotenza.
	Store Store

	// Sink è il consumatore delle push verificate: lo implementerà #422. Può
	// essere nil, e finché lo è le push vengono verificate e registrate come
	// [StatusIgnored] — ricevute, nessuno a cui darle.
	Sink PushSink

	Logger *slog.Logger

	// Now sostituisce l'orologio. Serve ai test.
	Now func() time.Time
}

// Service riceve le consegne del webhook GitHub. Va costruito con [NewService].
type Service struct {
	secret []byte
	store  Store
	sink   PushSink
	log    *slog.Logger
	now    func() time.Time
}

// NewService costruisce il servizio.
//
// Fallisce se il segreto manca: un webhook senza segreto non è un webhook meno
// sicuro, è un endpoint pubblico che accetta qualunque cosa. Chi non ha
// configurato la GitHub App non deve ottenere un servizio degradato ma nessun
// servizio, e la rotta non va registrata affatto.
func NewService(opts Options) (*Service, error) {
	secret := strings.TrimSpace(opts.Secret)
	switch {
	case secret == "":
		return nil, fmt.Errorf("githubhook: il segreto del webhook è obbligatorio (%s)", SecretEnvVar)
	case len(secret) < minSecretLen:
		return nil, fmt.Errorf("githubhook: il segreto del webhook è troppo corto: %d caratteri, minimo %d",
			len(secret), minSecretLen)
	case opts.Store == nil:
		return nil, errors.New("githubhook: Store è obbligatorio")
	}

	s := &Service{
		secret: []byte(secret),
		store:  opts.Store,
		sink:   opts.Sink,
		log:    opts.Logger,
		now:    opts.Now,
	}
	if s.log == nil {
		s.log = slog.Default()
	}
	if s.now == nil {
		s.now = time.Now
	}
	return s, nil
}

// HasSink indica se esiste un consumatore delle push. Serve a chi costruisce il
// servizio per dire nel log, all'avvio, che le push verranno solo registrate.
func (s *Service) HasSink() bool { return s.sink != nil }

// Receive verifica, registra e instrada una consegna.
//
// L'ordine delle operazioni è la sostanza di R11, non un dettaglio:
//
//  1. **la firma, prima di tutto il resto.** Prima di questo passaggio non
//     esiste niente di cui fidarsi, e non viene fatto niente: nessuna
//     decodifica, nessuna scrittura, nessun log del contenuto. Un endpoint
//     pubblico riceve anche ciò che qualcuno gli manda per provare;
//  2. la forma della richiesta, che ora si può contestare al chiamante perché la
//     firma dice chi è;
//  3. la registrazione della consegna, che è ciò che rende innocua la seconda
//     copia di un evento;
//  4. la consegna al consumatore, una volta sola.
//
// Il contesto passato dovrebbe sopravvivere alla disconnessione del chiamante:
// GitHub chiude la connessione dopo pochi secondi, e la registrazione dell'esito
// deve completare comunque.
func (s *Service) Receive(ctx context.Context, req Request) (Result, error) {
	if err := VerifySignature(s.secret, req.Body, req.Signature); err != nil {
		// Il log dice che una richiesta è stata rifiutata e **niente di ciò che
		// conteneva**: non il corpo, non le testate, non l'identificativo di
		// consegna. Sono tutti valori scelti da chi ha inviato la richiesta, e
		// riportarli significherebbe far scrivere nei nostri log a chiunque
		// conosca l'indirizzo dell'endpoint.
		s.log.WarnContext(ctx, "consegna GitHub rifiutata: firma non valida",
			slog.Bool("signature_present", req.Signature != ""))
		return Result{}, ErrInvalidSignature
	}

	// Da qui in poi la provenienza è provata: quello che segue viene da GitHub, e
	// si può nominare nei log e restituire al chiamante.
	delivery := strings.TrimSpace(req.Delivery)
	event := strings.TrimSpace(req.Event)
	switch {
	case delivery == "":
		return Result{}, fmt.Errorf("%w: testata %s assente", ErrInvalidRequest, HeaderDelivery)
	case len(delivery) > maxDeliveryIDLen:
		return Result{}, fmt.Errorf("%w: testata %s troppo lunga", ErrInvalidRequest, HeaderDelivery)
	case event == "":
		return Result{}, fmt.Errorf("%w: testata %s assente", ErrInvalidRequest, HeaderEvent)
	case len(event) > maxEventLen:
		return Result{}, fmt.Errorf("%w: testata %s troppo lunga", ErrInvalidRequest, HeaderEvent)
	}

	result := Result{Delivery: delivery, Event: event}

	// La push si decodifica prima di registrarla: la riga porta l'identità del
	// repository, e un payload illeggibile non ha niente da registrare. Un 400
	// qui è anche la risposta giusta — ripetere una consegna che non sappiamo
	// leggere non la renderà leggibile.
	var push PushEvent
	if event == EventPush {
		parsed, err := parsePush(delivery, req.Body)
		if err != nil {
			s.log.WarnContext(ctx, "consegna push GitHub non decodificabile",
				slog.String("delivery", delivery), slog.Any("error", err))
			return result, err
		}
		push = parsed
	}

	claimed, err := s.store.Claim(ctx, s.deliveryRow(delivery, event, push))
	if err != nil {
		return result, fmt.Errorf("githubhook: registrazione della consegna: %w", err)
	}
	if !claimed {
		// Seconda copia della stessa consegna. Nessun effetto, e la risposta è un
		// successo: dire a GitHub che è andata male otterrebbe altre copie.
		s.log.InfoContext(ctx, "consegna GitHub già ricevuta, ignorata",
			slog.String("delivery", delivery), slog.String("event", event))
		result.Outcome = OutcomeDuplicate
		return result, nil
	}

	switch event {
	case EventPush:
		return s.dispatch(ctx, result, push)
	case EventPing:
		s.complete(ctx, delivery, StatusIgnored, "")
		s.log.InfoContext(ctx, "webhook GitHub verificato dal ping dell'App",
			slog.String("delivery", delivery))
		result.Outcome = OutcomePong
		return result, nil
	default:
		// La App è sottoscritta al solo `push` (CREDENTIALS §5): un altro evento
		// significa che la sottoscrizione è cambiata. Va registrato e ignorato,
		// non rifiutato — rifiutarlo produrrebbe ripetizioni di qualcosa che non
		// ci serve.
		s.complete(ctx, delivery, StatusIgnored, "")
		s.log.InfoContext(ctx, "evento GitHub non trattato",
			slog.String("delivery", delivery), slog.String("event", event))
		result.Outcome = OutcomeIgnored
		return result, nil
	}
}

// dispatch passa la push al consumatore e ne registra l'esito.
func (s *Service) dispatch(ctx context.Context, result Result, push PushEvent) (Result, error) {
	if s.sink == nil {
		// #422 non c'è ancora: la consegna è verificata e registrata, e si ferma
		// qui. Lo stato lo dice per esteso, così che il registro non faccia
		// credere che qualcosa sia stato sincronizzato.
		s.complete(ctx, push.Delivery, StatusIgnored, "nessun consumatore degli eventi push configurato")
		s.log.InfoContext(ctx, "push GitHub registrata senza consumatore",
			slog.String("delivery", push.Delivery),
			slog.String("repository", push.Repository.FullName),
			slog.String("ref", push.Ref))
		result.Outcome = OutcomeIgnored
		return result, nil
	}

	if err := s.sink.HandlePush(ctx, push); err != nil {
		// Lo stato `failed` è ciò che permette a una ripetizione — automatica o
		// lanciata a mano dal registro dell'App — di rilavorare la consegna
		// invece di trovarla come duplicato. L'errore risale, e il livello HTTP
		// risponde 500: è la risposta che fa ripetere GitHub.
		s.complete(ctx, push.Delivery, StatusFailed, err.Error())
		s.log.ErrorContext(ctx, "lavorazione della push GitHub fallita",
			slog.String("delivery", push.Delivery),
			slog.String("repository", push.Repository.FullName),
			slog.Any("error", err))
		return result, fmt.Errorf("githubhook: lavorazione della consegna %s: %w", push.Delivery, err)
	}

	s.complete(ctx, push.Delivery, StatusProcessed, "")
	result.Outcome = OutcomeAccepted
	return result, nil
}

// complete registra l'esito senza farlo pesare sulla risposta.
//
// Un fallimento qui non cambia ciò che è già successo: il consumatore ha già
// lavorato l'evento, e rispondere 500 a GitHub farebbe ripetere una consegna il
// cui effetto c'è già stato. Resta nel log, che è il posto in cui si scopre che
// il database ha smesso di rispondere.
func (s *Service) complete(ctx context.Context, delivery string, status Status, failure string) {
	if err := s.store.Complete(ctx, delivery, status, failure, s.now()); err != nil {
		s.log.ErrorContext(ctx, "esito della consegna GitHub non registrato",
			slog.String("delivery", delivery),
			slog.String("status", string(status)),
			slog.Any("error", err))
	}
}

// deliveryRow compone la riga da registrare.
func (s *Service) deliveryRow(delivery, event string, push PushEvent) Delivery {
	row := Delivery{
		ID:                   delivery,
		Event:                event,
		InstallationID:       push.InstallationID,
		RepositoryExternalID: push.Repository.ExternalID,
		RepositoryFullName:   truncate(push.Repository.FullName, maxFullNameLen),
		Ref:                  truncate(push.Ref, maxRefLen),
		HeadCommit:           commitOrEmpty(push.After),
		ReceivedAt:           s.now(),
	}
	return row
}

// I limiti della migrazione 0011. Superarli farebbe fallire l'INSERT, e un
// INSERT fallito diventa un 500 che GitHub ripete: meglio registrare un valore
// tagliato che non registrare la consegna.
const (
	maxFullNameLen = 201
	maxRefLen      = 255
)

// commitSHA è il formato accettato dalla colonna `head_commit`.
var commitSHA = regexp.MustCompile(`^[0-9a-f]{7,40}$`)

// commitOrEmpty scarta un identificativo di commit che la 0011 rifiuterebbe.
// La perdita è trascurabile — resta comunque `ref` — e vale il non dover
// gestire un INSERT che fallisce su un payload malformato.
func commitOrEmpty(sha string) string {
	if commitSHA.MatchString(sha) {
		return sha
	}
	return ""
}

// truncate taglia a `limit` byte senza spezzare un carattere: mezza sequenza
// UTF-8 non è testo valido per PostgreSQL, e l'INSERT fallirebbe proprio nel
// caso che il taglio doveva evitare.
func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	cut := limit
	for cut > 0 && !utf8.ValidString(value[:cut]) {
		cut--
	}
	return value[:cut]
}
