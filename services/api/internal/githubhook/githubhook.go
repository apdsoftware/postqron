// Package githubhook riceve e verifica gli eventi del webhook GitHub (R11).
//
// Fa due cose, e deliberatamente solo quelle: **verifica la firma HMAC** del
// corpo grezzo e **registra la consegna** perché una ripetizione non produca un
// secondo effetto. Cosa fare del `cron.yaml` è #422, riconciliare i job è #423:
// il confine verso di loro è [PushSink], che questo package chiama e non
// implementa.
//
// Non dipende da pgx, per la stessa ragione di internal/apikeys: il rifiuto di
// una firma sbagliata e l'assenza di doppio effetto si devono poter provare
// senza un database in piedi. L'implementazione PostgreSQL di [Store] sta in
// internal/githubhookpg.
//
// # Perché il corpo grezzo
//
// GitHub firma i byte esatti che manda. Qualunque passaggio che li normalizzi
// prima del confronto — decodificare il JSON e riserializzarlo, riordinare i
// campi, togliere spazi — produce un HMAC diverso da quello firmato, e la
// verifica smette di dire qualcosa di vero. Per questo [Request.Body] è
// `[]byte` e non una struttura già decodificata: la decodifica avviene *dopo*
// la verifica, su byte di cui sappiamo già la provenienza.
package githubhook

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Testate del webhook GitHub. Sono costanti esportate perché il livello HTTP le
// legge dalla richiesta e questo package le nomina nei propri errori: due
// stringhe letterali che devono restare uguali sono una che diverge.
const (
	// HeaderSignature porta l'HMAC-SHA256 del corpo, nella forma `sha256=<hex>`.
	HeaderSignature = "X-Hub-Signature-256"
	// HeaderEvent porta il nome dell'evento (`push`, `ping`, ...).
	HeaderEvent = "X-GitHub-Event"
	// HeaderDelivery porta l'identificativo della consegna: è uguale per la
	// consegna originale e per ogni sua ripetizione, ed è la chiave
	// dell'idempotenza.
	HeaderDelivery = "X-GitHub-Delivery"
)

// Eventi trattati. Tutto il resto viene verificato, registrato e ignorato: la
// App è sottoscritta al solo `push` (CREDENTIALS §5), ma un endpoint pubblico
// deve reggere anche ciò che non si aspetta.
const (
	EventPush = "push"
	EventPing = "ping"
)

// Limiti di forma dei valori che arrivano dalle testate. Non sono controlli di
// sicurezza — la firma li ha già coperti — ma i vincoli della 0011: superarli
// significherebbe un INSERT rifiutato e un 500 che GitHub ripete all'infinito,
// quindi vengono fermati qui con un 400 che dice cosa non va.
const (
	maxDeliveryIDLen = 100
	maxEventLen      = 50
)

// Errori riconoscibili dal chiamante. Il livello HTTP li traduce in status: la
// corrispondenza vive lì, non qui.
var (
	// ErrInvalidSignature indica che la richiesta non è firmata, o non lo è con
	// il segreto giusto, o è stata alterata dopo la firma. **È lo stesso errore
	// per tutti e tre i casi**: distinguerli nella risposta racconterebbe a chi
	// prova come sta andando il tentativo.
	ErrInvalidSignature = errors.New("githubhook: firma non valida")

	// ErrInvalidRequest indica una richiesta firmata correttamente ma malformata
	// — testata obbligatoria assente, payload non decodificabile. Si può dire al
	// chiamante cosa manca: la firma prova che il chiamante è GitHub.
	ErrInvalidRequest = errors.New("githubhook: richiesta non valida")
)

// Status è lo stato di lavorazione di una consegna, uguale all'enumerato
// `github_delivery_status` della migrazione 0011.
type Status string

const (
	// StatusReceived è la presa in carico: la lavorazione è iniziata.
	StatusReceived Status = "received"
	// StatusProcessed è la consegna passata al [PushSink] senza errori.
	StatusProcessed Status = "processed"
	// StatusIgnored è la consegna verificata ma senza niente da fare.
	StatusIgnored Status = "ignored"
	// StatusFailed è la lavorazione fallita. È l'unico stato da cui una
	// ripetizione viene rilavorata: vedi [Store.Claim].
	StatusFailed Status = "failed"
)

// Outcome è l'esito che il chiamante comunica a GitHub.
type Outcome string

const (
	// OutcomeAccepted: push verificata e presa in carico dal consumatore.
	OutcomeAccepted Outcome = "accepted"
	// OutcomeDuplicate: consegna già ricevuta. Nessun secondo effetto.
	OutcomeDuplicate Outcome = "duplicate"
	// OutcomeIgnored: consegna verificata ma senza niente da fare.
	OutcomeIgnored Outcome = "ignored"
	// OutcomePong: risposta al `ping` con cui GitHub prova un webhook nuovo.
	OutcomePong Outcome = "pong"
)

// Request è una consegna così come arriva dal livello HTTP.
type Request struct {
	// Signature è il valore grezzo di [HeaderSignature].
	Signature string
	// Event è il valore di [HeaderEvent].
	Event string
	// Delivery è il valore di [HeaderDelivery].
	Delivery string
	// Body sono i byte esatti ricevuti, non decodificati e non normalizzati:
	// vedi la nota sul corpo grezzo nella documentazione del package.
	Body []byte
}

// Result è l'esito della ricezione.
type Result struct {
	Delivery string
	Event    string
	Outcome  Outcome
}

// Repository è il repository a cui una push si riferisce.
type Repository struct {
	// ExternalID è l'identificativo numerico lato GitHub: sopravvive a rinomine
	// e trasferimenti, mentre owner e nome no. È la chiave con cui #423 risale
	// alle righe di `repositories`.
	ExternalID int64
	Owner      string
	Name       string
	FullName   string
	// DefaultBranch arriva nel payload. Serve a #423, che decide se la push
	// riguarda il ramo sincronizzato; qui non filtra niente.
	DefaultBranch string
	Private       bool
}

// PushEvent è una push verificata, ridotta a ciò che serve per sincronizzare.
//
// **È il confine verso #422 e #423.** Contiene l'identità del repository,
// l'installazione con cui ottenere un token di lettura e il commit da leggere:
// con questi tre elementi #422 può scaricare `cron.yaml` e #423 riconciliare.
// Non contiene l'elenco dei file toccati né i commit: decidere se la push
// riguarda `cron.yaml` è una domanda sul contenuto del repository, e appartiene
// a chi quel contenuto lo legge.
type PushEvent struct {
	// Delivery è l'identificativo della consegna che ha portato l'evento: è la
	// chiave con cui ritrovarlo in `github_webhook_deliveries`.
	Delivery string

	// InstallationID è l'installazione della GitHub App che ha generato
	// l'evento. Senza, non si ottiene il token per leggere il file.
	InstallationID int64

	Repository Repository

	// Ref è il riferimento spinto, completo: `refs/heads/main`, `refs/tags/v1`.
	Ref string
	// Before e After sono i commit prima e dopo la push. After è quaranta zeri
	// quando il riferimento è stato cancellato.
	Before string
	After  string

	Created bool
	Deleted bool
	Forced  bool
}

// zeroCommit è il valore che GitHub usa per «nessun commit»: la cancellazione
// di un ramo arriva con `after` a quaranta zeri.
const zeroCommit = "0000000000000000000000000000000000000000"

// Branch restituisce il ramo spinto, o stringa vuota se il riferimento non è un
// ramo (un tag, per esempio).
func (e PushEvent) Branch() string {
	branch, ok := strings.CutPrefix(e.Ref, "refs/heads/")
	if !ok {
		return ""
	}
	return branch
}

// IsDefaultBranch indica se la push riguarda il ramo predefinito del
// repository. È il filtro che serve a #423 e che questo package non applica:
// registrare una push su un altro ramo costa una riga e permette di capire, a
// posteriori, perché un sync non è partito.
func (e PushEvent) IsDefaultBranch() bool {
	branch := e.Branch()
	return branch != "" && branch == e.Repository.DefaultBranch
}

// HasContent indica se dopo la push esiste un commit da leggere. È falso quando
// il riferimento è stato cancellato: non c'è nessun `cron.yaml` da scaricare.
func (e PushEvent) HasContent() bool {
	return !e.Deleted && e.After != "" && e.After != zeroCommit
}

// PushSink riceve le push già verificate e deduplicate.
//
// **È il confine dichiarato verso #422.** Questo package non lo implementa: qui
// finisce la ricezione, lì comincia la lettura di `cron.yaml`. L'implementazione
// dovrà tenere conto di due vincoli che nascono da questo lato del confine:
//
//   - GitHub chiude la connessione dopo pochi secondi, quindi il lavoro lungo va
//     accodato, non svolto dentro la chiamata;
//   - un errore restituito qui marca la consegna come fallita e fa rispondere
//     500, così che una ripetizione — automatica o lanciata a mano dal registro
//     dell'App — venga rilavorata invece che scartata come duplicato.
type PushSink interface {
	HandlePush(ctx context.Context, event PushEvent) error
}

// Delivery è la riga che registra una consegna ricevuta.
type Delivery struct {
	ID    string
	Event string

	InstallationID       int64
	RepositoryExternalID int64
	RepositoryFullName   string
	Ref                  string
	HeadCommit           string

	ReceivedAt time.Time
}

// Store conserva le consegne già viste. L'implementazione PostgreSQL è
// internal/githubhookpg.
type Store interface {
	// Claim registra la consegna e dice se è questa richiesta a doverla
	// lavorare.
	//
	// Restituisce false — senza errore — quando la consegna è già stata
	// ricevuta: è il punto in cui l'idempotenza di R11 diventa vera, e dev'essere
	// **una sola operazione atomica**, non una lettura seguita da una scrittura.
	// Due ripetizioni concorrenti della stessa consegna arrivano insieme, e con
	// due istruzioni separate passerebbero entrambe.
	//
	// L'unica eccezione è una consegna in stato [StatusFailed]: quella viene
	// riassegnata, perché è esattamente il caso per cui GitHub ripete.
	Claim(ctx context.Context, delivery Delivery) (bool, error)

	// Complete registra l'esito della lavorazione. `failure` è il motivo del
	// fallimento e va valorizzato se e solo se lo stato è [StatusFailed].
	Complete(ctx context.Context, deliveryID string, status Status, failure string, at time.Time) error
}

// ------------------------------------------------------------------ payload

// pushPayload è il sottoinsieme del payload `push` che ci interessa.
//
// Non usa DisallowUnknownFields, e non è una dimenticanza: GitHub aggiunge campi
// ai propri payload senza preavviso, e rifiutarne uno sconosciuto trasformerebbe
// un'aggiunta innocua in un webhook che smette di funzionare.
type pushPayload struct {
	Ref     string `json:"ref"`
	Before  string `json:"before"`
	After   string `json:"after"`
	Created bool   `json:"created"`
	Deleted bool   `json:"deleted"`
	Forced  bool   `json:"forced"`

	Repository struct {
		ID       int64  `json:"id"`
		Name     string `json:"name"`
		FullName string `json:"full_name"`
		Private  bool   `json:"private"`
		Owner    struct {
			Login string `json:"login"`
			Name  string `json:"name"`
		} `json:"owner"`
		DefaultBranch string `json:"default_branch"`
	} `json:"repository"`

	Installation struct {
		ID int64 `json:"id"`
	} `json:"installation"`
}

// parsePush decodifica il payload di una push già verificata.
//
// I controlli sono sui campi da cui dipende tutto il resto: senza identità del
// repository e senza riferimento non c'è niente da sincronizzare, e registrare
// una riga vuota servirebbe solo a far fallire l'INSERT più tardi.
func parsePush(delivery string, body []byte) (PushEvent, error) {
	var payload pushPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return PushEvent{}, fmt.Errorf("%w: payload push non decodificabile: %w", ErrInvalidRequest, err)
	}

	switch {
	case payload.Repository.ID <= 0:
		return PushEvent{}, fmt.Errorf("%w: payload push senza identificativo del repository", ErrInvalidRequest)
	case payload.Repository.FullName == "":
		return PushEvent{}, fmt.Errorf("%w: payload push senza nome del repository", ErrInvalidRequest)
	case payload.Ref == "":
		return PushEvent{}, fmt.Errorf("%w: payload push senza riferimento", ErrInvalidRequest)
	}

	owner := payload.Repository.Owner.Login
	if owner == "" {
		// `owner.login` è il campo canonico; `owner.name` compare in alcune
		// varianti del payload. Se mancano entrambi, il nome completo lo contiene
		// comunque.
		owner = payload.Repository.Owner.Name
	}
	if owner == "" {
		owner, _, _ = strings.Cut(payload.Repository.FullName, "/")
	}

	name := payload.Repository.Name
	if name == "" {
		_, name, _ = strings.Cut(payload.Repository.FullName, "/")
	}

	return PushEvent{
		Delivery:       delivery,
		InstallationID: payload.Installation.ID,
		Repository: Repository{
			ExternalID:    payload.Repository.ID,
			Owner:         owner,
			Name:          name,
			FullName:      payload.Repository.FullName,
			DefaultBranch: payload.Repository.DefaultBranch,
			Private:       payload.Repository.Private,
		},
		Ref:     payload.Ref,
		Before:  payload.Before,
		After:   payload.After,
		Created: payload.Created,
		Deleted: payload.Deleted,
		Forced:  payload.Forced,
	}, nil
}
