// Package legalpg è l'implementazione PostgreSQL di legal.Store.
//
// Sta in un package a parte per la stessa ragione di internal/authpg e
// internal/accountpg: internal/legal non deve dipendere da pgx, ed è ciò che
// permette di provare senza database la parte che ha più regole — quale versione
// vincola, cosa manca da accettare, cosa è stato annunciato.
//
// Qui restano tre istruzioni, e due delle tre esistono per una proprietà sola:
// **la prova non si riscrive**. La 0018 la difende con un trigger, questo
// package non le dà nemmeno un modo di provarci — non c'è un UPDATE, non c'è un
// DELETE, e l'inserimento è idempotente per non spostare la data di un consenso
// già prestato.
package legalpg

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/apdsoftware/postqron/services/api/internal/legal"
)

// Store implementa [legal.Store] su un pool pgx.
type Store struct {
	pool *pgxpool.Pool
}

// New costruisce lo store.
func New(pool *pgxpool.Pool) (*Store, error) {
	if pool == nil {
		return nil, errors.New("legalpg: il pool è obbligatorio")
	}
	return &Store{pool: pool}, nil
}

var _ legal.Store = (*Store)(nil)

const consentColumns = `document, version, language, document_checksum, source, accepted_at`

// ConsentsOf legge i consensi di un utente.
//
// L'ordine è per documento e per data: è l'ordine in cui si legge una storia, e
// serve alla risposta dell'API più che alla query. Il servizio riordina comunque
// secondo [legal.Documents], che è un ordine di senso e non alfabetico.
func (s *Store) ConsentsOf(ctx context.Context, userID string) ([]legal.Consent, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+consentColumns+`
		   FROM legal_consents
		  WHERE user_id = $1::uuid
		  ORDER BY document, accepted_at`, userID)
	if err != nil {
		return nil, fmt.Errorf("legalpg: lettura dei consensi: %w", err)
	}
	defer rows.Close()

	var out []legal.Consent
	for rows.Next() {
		var c legal.Consent
		if err := rows.Scan(&c.Document, &c.Version, &c.Language, &c.Checksum, &c.Source, &c.AcceptedAt); err != nil {
			return nil, fmt.Errorf("legalpg: lettura di un consenso: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("legalpg: lettura dei consensi: %w", err)
	}
	return out, nil
}

// Record registra i consensi in una transazione sola.
//
// # Perché `ON CONFLICT DO NOTHING` e non un `UPDATE`
//
// Perché la data di un consenso è l'istante in cui l'utente si è vincolato, e un
// doppio invio del form non deve poterla spostare in avanti. Un `ON CONFLICT DO
// UPDATE` — la forma che verrebbe naturale scrivere — farebbe esattamente
// quello: riscriverebbe la prova con la data della ripetizione. Il trigger della
// 0018 lo impedirebbe comunque, e va bene così: sono due difese della stessa
// cosa, e quella del database vale anche per chi non passa da qui.
//
// # Perché una transazione anche per quattro righe
//
// Perché le quattro accettazioni di una registrazione sono **un atto solo**: un
// account con il consenso ai Termini e non alla privacy policy sarebbe uno stato
// che nessuna schermata ha mai proposto a nessuno.
func (s *Store) Record(ctx context.Context, userID string, consents []legal.Consent) error {
	if len(consents) == 0 {
		return nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("legalpg: apertura della transazione: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	if err := RecordTx(ctx, tx, userID, consents); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("legalpg: conferma dei consensi: %w", err)
	}
	return nil
}

// insertConsentSQL è l'unica scrittura del package.
const insertConsentSQL = `
	INSERT INTO legal_consents (user_id, document, version, language, document_checksum, source, accepted_at)
	VALUES ($1::uuid, $2, $3, $4, $5, $6, $7)
	ON CONFLICT ON CONSTRAINT legal_consents_user_document_version_key DO NOTHING`

// RecordTx registra i consensi dentro una transazione già aperta.
//
// Esiste per la registrazione: internal/authpg crea l'utente e scrive i suoi
// consensi **nella stessa transazione**, perché un account senza la prova di ciò
// che ha accettato è uno stato che non deve poter esistere nemmeno per un
// istante. Senza questa funzione, authpg dovrebbe riscrivere l'istruzione, e le
// due copie divergerebbero alla prima colonna aggiunta.
func RecordTx(ctx context.Context, tx pgx.Tx, userID string, consents []legal.Consent) error {
	if len(consents) == 0 {
		return nil
	}
	batch := &pgx.Batch{}
	for _, c := range consents {
		batch.Queue(insertConsentSQL,
			userID, string(c.Document), c.Version, string(c.Language),
			c.Checksum, string(c.Source), c.AcceptedAt)
	}

	results := tx.SendBatch(ctx, batch)
	for _, c := range consents {
		if _, err := results.Exec(); err != nil {
			_ = results.Close()
			return fmt.Errorf("legalpg: registrazione del consenso a %s %s: %w", c.Document, c.Version, err)
		}
	}
	if err := results.Close(); err != nil {
		return fmt.Errorf("legalpg: registrazione dei consensi: %w", err)
	}
	return nil
}
