package legal

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"
)

// Store è la persistenza della prova del consenso.
//
// È un'interfaccia e non il pool di pgx per la stessa ragione di [auth.Store]:
// la logica di questo package — cosa manca da accettare, cosa è già stato
// accettato, cosa sta per cambiare — si prova senza database, e le query vivono
// in internal/legalpg dove si leggono tutte insieme.
//
// **Non c'è un metodo per cancellare o modificare un consenso**, e l'assenza è
// il contratto: una prova che si può ritirare non è una prova. L'unica
// cancellazione che esiste è quella dell'account, che porta via i consensi
// insieme a tutto il resto (R45) e non passa da qui.
type Store interface {
	// ConsentsOf legge i consensi di un utente, in qualunque ordine.
	ConsentsOf(ctx context.Context, userID string) ([]Consent, error)
	// Record registra consensi nuovi.
	//
	// È **idempotente sulla coppia (documento, versione)**: registrare due volte
	// la stessa accettazione non crea una seconda riga e non sposta la data
	// della prima. È la proprietà che conta di più di tutto il metodo — la data
	// di un consenso è il momento in cui l'utente si è vincolato, e un doppio
	// invio del form non deve poterla spostare in avanti.
	Record(ctx context.Context, userID string, consents []Consent) error
}

// Errori che il servizio distingue.
var (
	// ErrNoDocuments indica un'accettazione che non nomina nessun documento.
	ErrNoDocuments = errors.New("legal: nessun documento da accettare")
	// ErrDuplicateDocument indica lo stesso documento nominato due volte.
	ErrDuplicateDocument = errors.New("legal: documento ripetuto nella stessa accettazione")
)

// VersionNotInForceError dice quale versione è stata offerta e quale vincola
// davvero.
//
// I due numeri servono al client: la differenza fra «hai accettato una versione
// vecchia» e «hai accettato una versione che non è ancora in vigore» è la
// differenza fra ricaricare la pagina e aspettare, e senza entrambi i numeri
// nessuno può dirlo.
type VersionNotInForceError struct {
	Document Document
	// Offered è la versione che il client ha dichiarato di aver mostrato.
	Offered string
	// InForce è quella che vincola adesso.
	InForce string
}

func (e *VersionNotInForceError) Error() string {
	return fmt.Sprintf("legal: %s: accettata la %s, in vigore la %s", e.Document, e.Offered, e.InForce)
}

func (e *VersionNotInForceError) Is(target error) bool { return target == ErrVersionNotInForce }

// ------------------------------------------------------------------ servizio

// Options configura il [Service]. Store è obbligatorio.
type Options struct {
	Store  Store
	Logger *slog.Logger

	// Registry sostituisce [Current]. Serve ai test, che devono poter far
	// entrare in vigore una versione nuova senza aspettare il giorno.
	Registry *Registry

	// Now sostituisce l'orologio, per la stessa ragione.
	Now func() time.Time
}

// Service risponde a due domande e ne registra una terza: cosa ho accettato,
// cosa mi manca, e «accetto».
type Service struct {
	store    Store
	registry *Registry
	log      *slog.Logger
	now      func() time.Time
}

// New costruisce il servizio.
func New(opts Options) (*Service, error) {
	if opts.Store == nil {
		return nil, errors.New("legal: Store è obbligatorio")
	}
	s := &Service{
		store:    opts.Store,
		registry: opts.Registry,
		log:      opts.Logger,
		now:      opts.Now,
	}
	if s.registry == nil {
		s.registry = Current()
	}
	if s.log == nil {
		s.log = slog.Default()
	}
	if s.now == nil {
		s.now = time.Now
	}
	return s, nil
}

// Registry è il registro che il servizio applica.
func (s *Service) Registry() *Registry { return s.registry }

// Requirement è un documento in vigore che l'utente non ha ancora accettato.
type Requirement struct {
	Document Document
	Version  string
	// Language è la lingua in cui il testo verrà mostrato, decisa da
	// [Release.Presented] sulla preferenza dell'utente.
	Language  Language
	Effective time.Time
	// AcceptedVersion è la versione che l'utente aveva accettato prima, vuota se
	// non ne ha mai accettata una.
	//
	// È la differenza fra «non hai ancora accettato niente» e «hai accettato la
	// 1.2.0 e adesso vige la 1.3.0», che per l'utente sono due schermate diverse.
	AcceptedVersion string
}

// Change è un rilascio annunciato che non è ancora in vigore.
//
// È la metà dei Termini §9 che si può mostrare: «this changes on that day, and
// you may close your account before it takes effect».
type Change struct {
	Document  Document
	Version   string
	Effective time.Time
	Announced time.Time
	// Material dice se il cambiamento tocca materialmente i diritti dell'utente,
	// cioè se è quello a cui §9 lega i trenta giorni di preavviso.
	Material bool
}

// State è ciò che un utente può sapere dei propri consensi.
type State struct {
	// Accepted è la storia: ogni versione accettata, con quando e in che lingua.
	// Le versioni superate restano — sono la prova di cosa vincolava allora.
	Accepted []Consent
	// Outstanding sono i documenti in vigore non ancora accettati.
	Outstanding []Requirement
	// Upcoming sono i cambiamenti annunciati e non ancora in vigore.
	Upcoming []Change
}

// State racconta i consensi di un utente, nella lingua che preferisce.
//
// La lingua serve solo a [Requirement.Language]: dice in che lingua il testo da
// accettare verrà mostrato, che è ciò che il client deve sapere **prima** di
// mostrarlo. Sui consensi già prestati non ha effetto — quelli riportano la
// lingua che avevano allora, ed è tutto il punto di registrarla.
func (s *Service) State(ctx context.Context, userID string, preferred Language) (State, error) {
	accepted, err := s.store.ConsentsOf(ctx, userID)
	if err != nil {
		return State{}, fmt.Errorf("legal: lettura dei consensi: %w", err)
	}
	SortConsents(accepted)

	now := s.now()
	state := State{Accepted: accepted}

	for _, dr := range s.registry.InForceAll(now) {
		ultima, accettata := latestAccepted(accepted, dr.Document)
		if accettata && ultima == dr.Release.Version {
			continue
		}
		state.Outstanding = append(state.Outstanding, Requirement{
			Document:        dr.Document,
			Version:         dr.Release.Version,
			Language:        dr.Release.Presented(preferred),
			Effective:       dr.Release.Effective,
			AcceptedVersion: ultima,
		})
	}

	for _, dr := range s.registry.Upcoming(now) {
		state.Upcoming = append(state.Upcoming, Change{
			Document:  dr.Document,
			Version:   dr.Release.Version,
			Effective: dr.Release.Effective,
			Announced: dr.Release.Announced,
			Material:  dr.Release.Notice == NoticeMaterial,
		})
	}
	return state, nil
}

// Acceptance è un documento accettato, con la versione che il client dichiara di
// aver mostrato.
type Acceptance struct {
	Document Document
	Version  string
}

// AcceptInput è il corpo di un'accettazione.
type AcceptInput struct {
	// Language è la lingua in cui l'utente ha letto.
	Language Language
	// Accept elenca cosa accetta, documento per documento.
	Accept []Acceptance
}

// Accept registra i consensi e restituisce lo stato aggiornato.
//
// # Perché il client deve dire quale versione ha mostrato
//
// Perché altrimenti registreremmo un consenso su un testo che l'utente non ha
// visto. Fra il momento in cui la pagina si carica e quello in cui l'utente
// preme «accetto» può entrare in vigore una versione nuova: se il servizio
// scrivesse «ha accettato quella in vigore adesso», la prova direbbe una cosa
// falsa proprio nel caso in cui conta — quello in cui il testo è cambiato.
// Dichiarando la versione, il disallineamento diventa un rifiuto
// ([VersionNotInForceError]) e il client ricarica e la ripresenta.
//
// # Perché accettare due volte non fa niente
//
// Perché la data di un consenso è l'istante in cui l'utente si è vincolato, e
// non deve poter essere spostata da un doppio invio del form. Lo Store è
// idempotente sulla coppia (documento, versione): la seconda registrazione non
// crea una riga e non tocca la prima.
func (s *Service) Accept(ctx context.Context, userID string, in AcceptInput) (State, error) {
	if len(in.Accept) == 0 {
		return State{}, ErrNoDocuments
	}
	if !slices.Contains(Languages(), in.Language) {
		return State{}, fmt.Errorf("%w: %q", ErrUnknownLanguage, in.Language)
	}

	now := s.now()
	visti := make(map[Document]bool, len(in.Accept))
	consents := make([]Consent, 0, len(in.Accept))

	for _, a := range in.Accept {
		if !slices.Contains(Documents(), a.Document) {
			return State{}, fmt.Errorf("%w: %q", ErrUnknownDocument, a.Document)
		}
		if visti[a.Document] {
			return State{}, fmt.Errorf("%w: %s", ErrDuplicateDocument, a.Document)
		}
		visti[a.Document] = true

		rel, ok := s.registry.InForce(a.Document, now)
		if !ok || rel.Version != a.Version {
			// `ok` falso significa che nessuna versione di quel documento è
			// ancora in vigore: per il client è lo stesso caso — ha mostrato
			// qualcosa che non vincola — e la versione in vigore è «nessuna».
			return State{}, &VersionNotInForceError{
				Document: a.Document,
				Offered:  a.Version,
				InForce:  rel.Version,
			}
		}
		consents = append(consents, rel.consent(a.Document, in.Language, SourceReacceptance, now))
	}

	if err := s.store.Record(ctx, userID, consents); err != nil {
		return State{}, fmt.Errorf("legal: registrazione dei consensi: %w", err)
	}
	for _, c := range consents {
		// La riga che rende ricostruibile un consenso anche senza interrogare il
		// database: `user_id` non c'è, ma c'è tutto il resto di ciò che R46
		// chiede di poter dimostrare.
		s.log.InfoContext(ctx, "consenso registrato (R46)",
			slog.String("user_id", userID),
			slog.String("document", string(c.Document)),
			slog.String("version", c.Version),
			slog.String("language", string(c.Language)))
	}

	return s.State(ctx, userID, in.Language)
}

// latestAccepted è la versione più recente accettata per un documento.
func latestAccepted(consents []Consent, doc Document) (string, bool) {
	var ultima string
	var trovata bool
	for _, c := range consents {
		if c.Document != doc {
			continue
		}
		if !trovata || compareVersions(c.Version, ultima) > 0 {
			ultima, trovata = c.Version, true
		}
	}
	return ultima, trovata
}
