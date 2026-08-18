// Package marketingpg è l'implementazione PostgreSQL di [marketing.Store].
//
// Sta in un package a parte per la stessa ragione di internal/notifypg:
// internal/marketing non deve dipendere da pgx, ed è ciò che permette di provare
// il consenso, la firma del link e il rifiuto di mandare senza permesso senza un
// database in piedi.
//
// Qui non c'è politica: ci sono le query, e le due proprietà che **solo il
// database può garantire**.
//
// # Il consenso si legge insieme all'indirizzo
//
// [Store.Recipient] restituisce indirizzo, lingua e consenso da **una sola
// istruzione**. Non è un'ottimizzazione: leggere l'indirizzo e poi chiedere il
// consenso lascerebbe in mezzo una finestra in cui l'utente si disiscrive e
// l'email parte lo stesso. Con una lettura sola quella finestra non esiste.
//
// # La traccia si allunga solo quando cambia qualcosa
//
// [Store.Record] è **una sola istruzione** che decide anche se scrivere: la
// condizione sta nella `WHERE` dell'`INSERT`, non in un «leggi lo stato, e se è
// diverso inserisci» sparso fra due andate al database. La traccia deve
// raccontare le volte in cui l'utente ha cambiato idea, non le volte in cui un
// browser ha rinviato un form.
//
// **Non è una serializzazione, e va detto.** Sotto `READ COMMITTED` due
// transazioni concorrenti leggono entrambe lo stato precedente e possono
// inserire entrambe: la finestra si riduce alla durata dell'istruzione, non
// sparisce. Non la si chiude — servirebbe un lock sulla riga dell'utente su un
// percorso che serve solo a questo — perché il caso peggiore è **due righe
// identiche a millisecondi di distanza in una traccia append-only**, cioè un
// duplicato onesto: dice che due richieste sono arrivate insieme, che è quello
// che è successo. Lo stato risultante è lo stesso, e nessuna delle due direzioni
// dell'errore che contano — mandare senza consenso, o continuare dopo una
// revoca — diventa possibile.
package marketingpg

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/apdsoftware/postqron/services/api/internal/emailrender"
	"github.com/apdsoftware/postqron/services/api/internal/marketing"
)

// Store implementa [marketing.Store] su un pool pgx.
type Store struct {
	pool *pgxpool.Pool
}

// New costruisce lo store.
func New(pool *pgxpool.Pool) (*Store, error) {
	if pool == nil {
		return nil, errors.New("marketingpg: il pool è obbligatorio")
	}
	return &Store{pool: pool}, nil
}

var _ marketing.Store = (*Store)(nil)

// NewService compone il servizio del consenso sul pool dato.
func NewService(pool *pgxpool.Pool, signer marketing.Signer, apiBaseURL string, logger *slog.Logger) (*marketing.Service, error) {
	store, err := New(pool)
	if err != nil {
		return nil, err
	}
	return marketing.NewService(marketing.Options{
		Store:      store,
		Signer:     signer,
		APIBaseURL: apiBaseURL,
		Logger:     logger,
	})
}

// ---------------------------------------------------------------- decisione

// recordSQL scrive una decisione, se cambia lo stato e se l'utente c'è.
//
// Le tre parti, in ordine:
//
//   - `target` è la verifica del destinatario. Un account cancellato non
//     decide più niente.
//   - l'`INSERT ... SELECT ... WHERE` è la condizione: si scrive solo se
//     l'ultima decisione è diversa da questa. Il `coalesce` su `'withdrawn'` è
//     la regola dell'Art. 6(1)(a) scritta in SQL — **chi non ha mai deciso non
//     ha acconsentito** — e sta qui invece che in Go perché è la stessa regola
//     che la vista `marketing_consent_state` applica leggendo.
//   - il `SELECT` finale distingue i tre esiti senza una seconda andata al
//     database.
const recordSQL = `
WITH target AS (
    SELECT u.id
      FROM users u
     WHERE u.id = $1::uuid
       AND u.deleted_at IS NULL
),
written AS (
    INSERT INTO marketing_consents (user_id, decision, source, ip_address)
    SELECT t.id,
           $2::text::marketing_consent_decision,
           $3::text::marketing_consent_source,
           $4::inet
      FROM target t
     WHERE coalesce(
               (SELECT s.decision FROM marketing_consent_state s WHERE s.user_id = t.id),
               'withdrawn'::marketing_consent_decision
           ) <> $2::text::marketing_consent_decision
    RETURNING id
)
SELECT EXISTS (SELECT 1 FROM target)  AS has_user,
       EXISTS (SELECT 1 FROM written) AS written`

// Record implementa [marketing.Store].
func (s *Store) Record(ctx context.Context, record marketing.Record) (marketing.Applied, error) {
	if err := record.Validate(); err != nil {
		return "", err
	}

	var hasUser, written bool
	err := s.pool.QueryRow(ctx, recordSQL,
		record.UserID,
		string(record.Decision),
		string(record.Source),
		nullAddr(record.IP),
	).Scan(&hasUser, &written)
	if err != nil {
		return "", fmt.Errorf("marketingpg: registrazione della decisione: %w", err)
	}

	switch {
	case !hasUser:
		return marketing.NoUser, nil
	case written:
		return marketing.Recorded, nil
	default:
		return marketing.Unchanged, nil
	}
}

// ------------------------------------------------------------------- stato

// State implementa [marketing.Store].
//
// Nessuna riga significa «non ha mai deciso», che non è la stessa cosa di «ha
// detto di no» — e non è nemmeno un permesso: entrambi si traducono in nessun
// invio, ma la dashboard li mostra in modo diverso.
func (s *Store) State(ctx context.Context, userID string) (marketing.State, error) {
	state := marketing.State{UserID: userID}

	var decision, source string
	var occurredAt time.Time
	err := s.pool.QueryRow(ctx,
		`SELECT s.decision::text, s.source::text, s.occurred_at
		   FROM marketing_consent_state s
		  WHERE s.user_id = $1::uuid`,
		userID).Scan(&decision, &source, &occurredAt)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return state, nil
	case err != nil:
		return marketing.State{}, fmt.Errorf("marketingpg: lettura del consenso: %w", err)
	}

	state.Decided = true
	state.Consented = marketing.Decision(decision) == marketing.DecisionGranted
	state.OccurredAt = occurredAt
	state.Source = marketing.Source(source)
	return state, nil
}

// History implementa [marketing.Store].
func (s *Store) History(ctx context.Context, userID string) ([]marketing.Entry, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT c.decision::text, c.source::text, c.occurred_at
		   FROM marketing_consents c
		  WHERE c.user_id = $1::uuid
		  ORDER BY c.occurred_at DESC, c.id DESC`,
		userID)
	if err != nil {
		return nil, fmt.Errorf("marketingpg: lettura della traccia del consenso: %w", err)
	}
	defer rows.Close()

	var entries []marketing.Entry
	for rows.Next() {
		var decision, source string
		var occurredAt time.Time
		if err := rows.Scan(&decision, &source, &occurredAt); err != nil {
			return nil, fmt.Errorf("marketingpg: lettura di una decisione: %w", err)
		}
		entries = append(entries, marketing.Entry{
			Decision:   marketing.Decision(decision),
			Source:     marketing.Source(source),
			OccurredAt: occurredAt,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("marketingpg: lettura della traccia del consenso: %w", err)
	}
	return entries, nil
}

// -------------------------------------------------------------- destinatario

// recipientSQL legge destinatario **e** consenso.
//
// Le tre condizioni del `WHERE` sono tre modi diversi di non avere nessuno a cui
// scrivere, e il terzo merita di essere detto: un account nella finestra di
// ripensamento della 0017 ha chiesto di andarsene. Continuare a mandargli
// comunicazioni di prodotto in quei trenta giorni sarebbe la lettura più
// ottusa possibile del consenso che aveva prestato — formalmente in vigore,
// sostanzialmente ritirato dal gesto più forte che poteva fare. Le email
// transazionali invece continuano, ed è giusto: raccontano proprio la
// cancellazione in corso.
const recipientSQL = `
SELECT u.id::text,
       u.email,
       coalesce(u.full_name, ''),
       u.language,
       coalesce(s.decision = 'granted', false)
  FROM users u
  LEFT JOIN marketing_consent_state s ON s.user_id = u.id
 WHERE u.id = $1::uuid
   AND u.deleted_at IS NULL
   AND u.deletion_requested_at IS NULL`

// Recipient implementa [marketing.Store].
func (s *Store) Recipient(ctx context.Context, userID string) (marketing.Recipient, error) {
	var r marketing.Recipient
	err := s.pool.QueryRow(ctx, recipientSQL, userID).
		Scan(&r.UserID, &r.Email, &r.Name, &r.Language, &r.Consented)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return marketing.Recipient{}, marketing.ErrNoRecipient
	case err != nil:
		return marketing.Recipient{}, fmt.Errorf("marketingpg: lettura del destinatario: %w", err)
	}

	// La colonna ha un CHECK sulle cinque lingue, ma la normalizzazione è
	// gratuita e toglie di mezzo il caso di una riga scritta prima del vincolo.
	r.Language = emailrender.NormalizeLanguage(r.Language)
	return r, nil
}

// Language implementa [marketing.Store].
//
// Il `WHERE` ha **una condizione sola**, ed è la differenza che conta rispetto a
// [Store.Recipient]: chi ha chiesto la cancellazione dell'account compare qui e
// non là. Dirgli che il link di disiscrizione non funziona proprio mentre sta
// chiudendo l'account sarebbe il momento peggiore per farlo, e §2.8 non pone
// condizioni alla revoca.
//
// Il `SELECT` non nomina `u.email`, e non è una svista da correggere: la pagina
// di disiscrizione la apre chiunque abbia un link, e ciò che non viene letto non
// può finirci dentro per distrazione.
func (s *Store) Language(ctx context.Context, userID string) (string, error) {
	var language string
	err := s.pool.QueryRow(ctx,
		`SELECT u.language FROM users u WHERE u.id = $1::uuid AND u.deleted_at IS NULL`,
		userID).Scan(&language)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return "", marketing.ErrNoRecipient
	case err != nil:
		return "", fmt.Errorf("marketingpg: lettura della lingua del profilo: %w", err)
	}
	return emailrender.NormalizeLanguage(language), nil
}

// ---------------------------------------------------------------- utilità

// nullAddr traduce un indirizzo assente o illeggibile in NULL.
//
// Un valore che non si analizza **non fa fallire la registrazione della
// decisione**, e la scelta è deliberata: l'indirizzo è un contorno della prova,
// la decisione è la prova. Rifiutare una revoca perché la testata che porta
// l'indirizzo era malformata significherebbe continuare a scrivere a chi ha
// chiesto di smettere, per un dettaglio che non lo riguarda.
func nullAddr(value string) *netip.Addr {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	addr, err := netip.ParseAddr(value)
	if err != nil {
		return nil
	}
	addr = addr.Unmap()
	return &addr
}
