package paddle

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// Options configura il [Service]. Secret e Store sono obbligatori.
type Options struct {
	// Secret è il segreto di firma delle notifiche Paddle.
	Secret string

	// Store registra gli eventi già ricevuti. Senza, non c'è idempotenza.
	Store Store

	// Sink applica le sottoscrizioni verificate: è internal/billing. Può essere
	// nil, e finché lo è gli eventi vengono verificati e registrati come
	// [StatusIgnored] — ricevuti, nessuno a cui darli.
	Sink EntitlementSink

	// Tolerance sostituisce [DefaultTolerance]. Serve ai test, che devono poter
	// costruire una consegna vecchia senza aspettare.
	Tolerance time.Duration

	Logger *slog.Logger

	// Now sostituisce l'orologio. Serve ai test.
	Now func() time.Time
}

// Service riceve le consegne del webhook Paddle. Va costruito con [NewService].
type Service struct {
	secret    []byte
	store     Store
	sink      EntitlementSink
	tolerance time.Duration
	log       *slog.Logger
	now       func() time.Time
}

// NewService costruisce il servizio.
//
// Fallisce se il segreto manca: un webhook di fatturazione senza segreto non è
// un webhook meno sicuro, è un modo per farsi regalare un piano a pagamento da
// chiunque conosca l'indirizzo. Chi non ha configurato Paddle non deve ottenere
// un servizio degradato ma nessun servizio, e la rotta non va registrata
// affatto.
func NewService(opts Options) (*Service, error) {
	secret := strings.TrimSpace(opts.Secret)
	switch {
	case secret == "":
		return nil, fmt.Errorf("paddle: il segreto del webhook è obbligatorio (%s)", SecretEnvVar)
	case len(secret) < minSecretLen:
		return nil, fmt.Errorf("paddle: il segreto del webhook è troppo corto: %d caratteri, minimo %d",
			len(secret), minSecretLen)
	case opts.Store == nil:
		return nil, errors.New("paddle: Store è obbligatorio")
	}

	s := &Service{
		secret:    []byte(secret),
		store:     opts.Store,
		sink:      opts.Sink,
		tolerance: opts.Tolerance,
		log:       opts.Logger,
		now:       opts.Now,
	}
	if s.tolerance == 0 {
		s.tolerance = DefaultTolerance
	}
	if s.log == nil {
		s.log = slog.Default()
	}
	if s.now == nil {
		s.now = time.Now
	}
	return s, nil
}

// HasSink indica se esiste un consumatore degli entitlement. Serve a chi
// costruisce il servizio per dire nel log, all'avvio, che gli eventi verranno
// solo registrati.
func (s *Service) HasSink() bool { return s.sink != nil }

// Receive verifica, registra e applica una consegna.
//
// L'ordine delle operazioni è la sostanza di R16, non un dettaglio:
//
//  1. **la firma, prima di tutto il resto.** Prima di questo passaggio non
//     esiste niente di cui fidarsi, e non viene fatto niente: nessuna
//     decodifica, nessuna scrittura, nessun log del contenuto. Un endpoint
//     pubblico di fatturazione riceve anche ciò che qualcuno gli manda per
//     provare, e il premio in palio è un piano a pagamento;
//  2. la forma del payload, che ora si può contestare al chiamante perché la
//     firma dice chi è;
//  3. la registrazione dell'evento, che è ciò che rende innocua la seconda copia
//     di una consegna;
//  4. l'applicazione all'entitlement, una volta sola — e solo se l'evento non è
//     più vecchio di ciò che è già in forza.
//
// I punti 3 e 4 sono difese **distinte** e servono a due guasti diversi. La
// terza ferma la stessa consegna che arriva due volte; la quarta ferma una
// consegna diversa e più vecchia che arriva dopo una più recente — che è nuova,
// legittima e firmata, e che senza filigrana riporterebbe in vita un piano che
// l'utente non ha più. È il punto in cui questa integrazione si rompe in
// produzione, non la firma.
//
// Il contesto passato dovrebbe sopravvivere alla disconnessione del chiamante:
// Paddle chiude la connessione dopo pochi secondi, e la registrazione dell'esito
// deve completare comunque.
func (s *Service) Receive(ctx context.Context, req Request) (Result, error) {
	if err := VerifySignature(s.secret, req.Body, req.Signature, s.now(), s.tolerance); err != nil {
		// Il log dice che una richiesta è stata rifiutata e **niente di ciò che
		// conteneva**: non il corpo, non le testate, non l'identificativo
		// dell'evento. Sono tutti valori scelti da chi ha inviato la richiesta, e
		// riportarli significherebbe far scrivere nei nostri log a chiunque
		// conosca l'indirizzo dell'endpoint — con dentro, per di più, quello che
		// quel qualcuno ha deciso che sembri un dato di fatturazione.
		s.log.WarnContext(ctx, "consegna Paddle rifiutata: firma non valida",
			slog.Bool("signature_present", req.Signature != ""))
		return Result{}, ErrInvalidSignature
	}

	// Da qui in poi la provenienza è provata: quello che segue viene da Paddle, e
	// si può nominare nei log e restituire al chiamante.
	event, data, err := parseEvent(req.Body)
	if err != nil {
		s.log.WarnContext(ctx, "consegna Paddle non decodificabile", slog.Any("error", err))
		return Result{}, err
	}

	result := Result{EventID: event.ID, Type: event.Type}
	isSubscription := strings.HasPrefix(event.Type, EventPrefixSubscription)

	// La sottoscrizione si decodifica **prima** di registrarla: un payload
	// illeggibile non ha niente da applicare, e un 400 qui è anche la risposta
	// giusta — ripetere una consegna che non sappiamo leggere non la renderà
	// leggibile. Registrarla prima lascerebbe inoltre una riga che dichiara di
	// aver preso in carico un evento mai lavorabile.
	var sub Subscription
	if isSubscription {
		parsed, err := parseSubscription(event, data)
		if err != nil {
			s.log.WarnContext(ctx, "sottoscrizione Paddle non decodificabile",
				slog.String("event_id", event.ID),
				slog.String("event_type", event.Type),
				slog.Any("error", err))
			return result, err
		}
		sub = parsed
	}

	subscriptionID, customerID := sub.ID, sub.CustomerID
	if !isSubscription {
		subscriptionID, customerID = parseIdentity(event.Type, data)
	}

	claimed, err := s.store.Claim(ctx, Record{
		ID:             event.ID,
		Type:           event.Type,
		OccurredAt:     event.OccurredAt,
		SubscriptionID: subscriptionID,
		CustomerID:     customerID,
		ReceivedAt:     s.now(),
	})
	if err != nil {
		return result, fmt.Errorf("paddle: registrazione dell'evento: %w", err)
	}
	if !claimed {
		// Seconda copia dello stesso evento. Nessun effetto, e la risposta è un
		// successo: dire a Paddle che è andata male otterrebbe altre copie.
		s.log.InfoContext(ctx, "evento Paddle già ricevuto, ignorato",
			slog.String("event_id", event.ID), slog.String("event_type", event.Type))
		result.Outcome = OutcomeDuplicate
		return result, nil
	}

	if !isSubscription {
		// Transazioni, prodotti, prezzi, aggiustamenti: verificati, registrati e
		// non applicati. Un pagamento fallito **non** cambia il piano — vedi
		// [EventPrefixSubscription], e i Termini §4.2 che lo promettono per
		// iscritto.
		s.complete(ctx, event.ID, StatusIgnored, "")
		s.log.InfoContext(ctx, "evento Paddle non trattato",
			slog.String("event_id", event.ID), slog.String("event_type", event.Type))
		result.Outcome = OutcomeIgnored
		return result, nil
	}

	return s.apply(ctx, result, sub)
}

// apply passa la sottoscrizione al consumatore e ne registra l'esito.
func (s *Service) apply(ctx context.Context, result Result, sub Subscription) (Result, error) {
	if s.sink == nil {
		// Nessun consumatore configurato: l'evento è verificato e registrato, e si
		// ferma qui. Lo stato lo dice per esteso, così che il registro non faccia
		// credere che un piano sia stato aggiornato.
		s.complete(ctx, sub.Event.ID, StatusIgnored, "nessun consumatore degli entitlement configurato")
		s.log.InfoContext(ctx, "sottoscrizione Paddle registrata senza consumatore",
			slog.String("event_id", sub.Event.ID),
			slog.String("subscription_id", sub.ID),
			slog.String("status", string(sub.Status)))
		result.Outcome = OutcomeIgnored
		return result, nil
	}

	applied, err := s.sink.ApplySubscription(ctx, sub)
	if err != nil {
		// Lo stato `failed` è ciò che permette a una ripetizione — automatica o
		// lanciata a mano dal cruscotto di Paddle — di rilavorare l'evento invece
		// di trovarlo come duplicato. L'errore risale, e il livello HTTP risponde
		// 500: è la risposta che fa ripetere Paddle.
		s.complete(ctx, sub.Event.ID, StatusFailed, err.Error())
		s.log.ErrorContext(ctx, "applicazione della sottoscrizione Paddle fallita",
			slog.String("event_id", sub.Event.ID),
			slog.String("subscription_id", sub.ID),
			slog.Any("error", err))
		return result, fmt.Errorf("paddle: lavorazione dell'evento %s: %w", sub.Event.ID, err)
	}

	if !applied {
		// L'evento è arrivato dopo uno più recente. Registrato come ignorato e
		// **non** come fallito: non c'è niente da rilavorare, e marcarlo fallito
		// significherebbe farlo riprovare all'infinito da una consegna che si
		// comporta correttamente.
		s.complete(ctx, sub.Event.ID, StatusIgnored, "evento più vecchio di quanto già applicato")
		s.log.InfoContext(ctx, "evento Paddle fuori ordine, non applicato",
			slog.String("event_id", sub.Event.ID),
			slog.String("subscription_id", sub.ID),
			slog.Time("occurred_at", sub.Event.OccurredAt))
		result.Outcome = OutcomeStale
		return result, nil
	}

	s.complete(ctx, sub.Event.ID, StatusProcessed, "")
	result.Outcome = OutcomeApplied
	return result, nil
}

// complete registra l'esito senza farlo pesare sulla risposta.
//
// Un fallimento qui non cambia ciò che è già successo: l'entitlement è già
// stato aggiornato, e rispondere 500 a Paddle farebbe ripetere una consegna il
// cui effetto c'è già stato. Resta nel log, che è il posto in cui si scopre che
// il database ha smesso di rispondere.
func (s *Service) complete(ctx context.Context, eventID string, status Status, failure string) {
	if err := s.store.Complete(ctx, eventID, status, failure, s.now()); err != nil {
		s.log.ErrorContext(ctx, "esito dell'evento Paddle non registrato",
			slog.String("event_id", eventID),
			slog.String("status", string(status)),
			slog.Any("error", err))
	}
}
