package migrate

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"text/tabwriter"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// TableName è la tabella in cui il tool registra ciò che ha applicato. Non
// nasce da una migrazione: sarebbe la migrazione che tiene traccia di sé stessa.
const TableName = "schema_migrations"

// advisoryLockKey identifica il lock consultivo che serializza le migrazioni.
// Il valore è la parola POSTQRON in ASCII: qualunque costante andrebbe bene,
// una riconoscibile in `pg_locks` è più utile quando si indaga un blocco.
const advisoryLockKey int64 = 0x504F535451524F4E

const (
	lockTimeout       = 30 * time.Second
	lockRetryInterval = 500 * time.Millisecond
)

// Conn è la parte di pgx che serve al migratore. Il tipo è un'interfaccia per
// non vincolare il chiamante fra una connessione singola e una presa dal pool.
//
// Deve però essere **una sola sessione** per tutta la durata dell'operazione: i
// lock consultivi sono legati alla sessione, e da una seconda connessione non
// sarebbero nemmeno visibili.
type Conn interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Begin(ctx context.Context) (pgx.Tx, error)
}

// Record è una migrazione già applicata, come risulta al database.
type Record struct {
	Version    int
	Name       string
	Checksum   string
	AppliedAt  time.Time
	DurationMS int
}

// Migrator applica e annulla le migrazioni su una connessione.
type Migrator struct {
	conn       Conn
	migrations []Migration
	out        io.Writer
}

// New crea un migratore sulle migrazioni indicate, che devono essere ordinate
// per versione crescente (è ciò che restituisce Load).
func New(conn Conn, migrations []Migration, out io.Writer) *Migrator {
	if out == nil {
		out = io.Discard
	}
	return &Migrator{conn: conn, migrations: migrations, out: out}
}

// WithLock esegue fn con il lock consultivo delle migrazioni.
//
// Serializza due migratori concorrenti — due deploy simultanei, o un deploy e
// una mano umana in psql. Senza, entrambi vedrebbero la stessa lista di
// pendenti e proverebbero ad applicarle: il secondo fallirebbe a metà, con lo
// schema in uno stato che nessuno dei due ha previsto.
func (m *Migrator) WithLock(ctx context.Context, fn func(context.Context) error) error {
	if err := m.lock(ctx); err != nil {
		return err
	}
	defer func() {
		// Il rilascio usa un contesto proprio: se fn è terminata perché il
		// contesto è stato annullato, un unlock su quel contesto fallirebbe e
		// lascerebbe il lock appeso fino alla chiusura della sessione.
		unlockCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if _, err := m.conn.Exec(unlockCtx, "SELECT pg_advisory_unlock($1)", advisoryLockKey); err != nil {
			fmt.Fprintf(m.out, "attenzione: rilascio del lock fallito: %v\n", err)
		}
	}()
	return fn(ctx)
}

func (m *Migrator) lock(ctx context.Context) error {
	deadline := time.Now().Add(lockTimeout)
	for {
		var acquired bool
		if err := m.conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", advisoryLockKey).Scan(&acquired); err != nil {
			return fmt.Errorf("acquisizione del lock delle migrazioni: %w", err)
		}
		if acquired {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf(
				"lock delle migrazioni non ottenuto entro %s: un'altra migrazione è in corso sullo stesso database",
				lockTimeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(lockRetryInterval):
		}
	}
}

// EnsureTable crea la tabella di stato se manca.
func (m *Migrator) EnsureTable(ctx context.Context) error {
	const stmt = `
CREATE TABLE IF NOT EXISTS ` + TableName + ` (
    version integer PRIMARY KEY,
    name text NOT NULL,
    checksum text NOT NULL,
    applied_at timestamptz NOT NULL DEFAULT now(),
    duration_ms integer NOT NULL
)`
	if _, err := m.conn.Exec(ctx, stmt); err != nil {
		return fmt.Errorf("creazione di %s: %w", TableName, err)
	}
	return nil
}

// Applied restituisce le migrazioni registrate, in ordine di versione.
func (m *Migrator) Applied(ctx context.Context) ([]Record, error) {
	rows, err := m.conn.Query(ctx,
		`SELECT version, name, checksum, applied_at, duration_ms FROM `+TableName+` ORDER BY version`)
	if err != nil {
		return nil, fmt.Errorf("lettura di %s: %w", TableName, err)
	}
	defer rows.Close()

	var records []Record
	for rows.Next() {
		var r Record
		if err := rows.Scan(&r.Version, &r.Name, &r.Checksum, &r.AppliedAt, &r.DurationMS); err != nil {
			return nil, fmt.Errorf("lettura di %s: %w", TableName, err)
		}
		records = append(records, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("lettura di %s: %w", TableName, err)
	}
	return records, nil
}

// Verify confronta lo stato registrato con i file su disco.
//
// Sono i tre modi in cui la storia delle migrazioni può essere stata riscritta,
// e vanno tutti fermati prima di toccare lo schema — non dopo.
func (m *Migrator) Verify(records []Record) error {
	byVersion := make(map[int]Migration, len(m.migrations))
	for _, mig := range m.migrations {
		byVersion[mig.Version] = mig
	}

	highest := 0
	for _, record := range records {
		mig, ok := byVersion[record.Version]
		if !ok {
			return fmt.Errorf(
				"%04d_%s risulta applicata nel database ma il file non esiste più: %w",
				record.Version, record.Name, ErrNotFound)
		}
		if mig.Checksum != record.Checksum {
			return fmt.Errorf(
				"%s è stata modificata dopo essere stata applicata (checksum %s, atteso %s): "+
					"una migrazione già applicata non si riscrive, si corregge con una nuova migrazione "+
					"(db/migrations/README.md)",
				mig, mig.Checksum, record.Checksum)
		}
		highest = max(highest, record.Version)
	}

	applied := make(map[int]bool, len(records))
	for _, record := range records {
		applied[record.Version] = true
	}
	for _, mig := range m.migrations {
		if !applied[mig.Version] && mig.Version < highest {
			return fmt.Errorf(
				"%s è pendente ma la sua versione precede la %04d già applicata: "+
					"applicarla ora la eseguirebbe fuori ordine. Rinumerala dopo l'ultima applicata",
				mig, highest)
		}
	}
	return nil
}

// Pending restituisce le migrazioni non ancora applicate, in ordine.
func (m *Migrator) Pending(ctx context.Context) ([]Migration, error) {
	records, err := m.Applied(ctx)
	if err != nil {
		return nil, err
	}
	if err := m.Verify(records); err != nil {
		return nil, err
	}
	applied := make(map[int]bool, len(records))
	for _, record := range records {
		applied[record.Version] = true
	}

	var pending []Migration
	for _, mig := range m.migrations {
		if !applied[mig.Version] {
			pending = append(pending, mig)
		}
	}
	return pending, nil
}

// Up applica le migrazioni pendenti e restituisce quante ne ha applicate.
// Un limit maggiore di zero ne applica al massimo quel numero.
func (m *Migrator) Up(ctx context.Context, limit int) (int, error) {
	if err := m.EnsureTable(ctx); err != nil {
		return 0, err
	}
	pending, err := m.Pending(ctx)
	if err != nil {
		return 0, err
	}
	if limit > 0 && len(pending) > limit {
		pending = pending[:limit]
	}
	if len(pending) == 0 {
		fmt.Fprintln(m.out, "schema già aggiornato: nessuna migrazione da applicare")
		return 0, nil
	}

	for i, mig := range pending {
		if err := m.apply(ctx, mig); err != nil {
			return i, err
		}
	}
	fmt.Fprintln(m.out, pluralize(len(pending), "migrazione applicata", "migrazioni applicate"))
	return len(pending), nil
}

// pluralize formatta «1 migrazione applicata» invece di «1 migrazioni applicate».
func pluralize(n int, singular, plural string) string {
	if n == 1 {
		return "1 " + singular
	}
	return fmt.Sprintf("%d %s", n, plural)
}

func (m *Migrator) apply(ctx context.Context, mig Migration) error {
	tx, err := m.conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("%s: apertura della transazione: %w", mig, err)
	}
	// Il rollback su una transazione già confermata è un no-op: serve solo a
	// garantire che un'uscita per errore non lasci la transazione aperta.
	defer func() { _ = tx.Rollback(ctx) }()

	start := time.Now()
	if _, err := tx.Exec(ctx, mig.Up); err != nil {
		return fmt.Errorf("%s: %w", mig, err)
	}
	elapsed := time.Since(start)

	if _, err := tx.Exec(ctx,
		`INSERT INTO `+TableName+` (version, name, checksum, duration_ms) VALUES ($1, $2, $3, $4)`,
		mig.Version, mig.Name, mig.Checksum, elapsed.Milliseconds(),
	); err != nil {
		return fmt.Errorf("%s: registrazione in %s: %w", mig, TableName, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("%s: commit: %w", mig, err)
	}

	fmt.Fprintf(m.out, "  ↑ %s (%s)\n", mig, elapsed.Round(time.Millisecond))
	return nil
}

// Down annulla le ultime count migrazioni applicate, dalla più recente alla più
// vecchia, e restituisce quante ne ha annullate.
func (m *Migrator) Down(ctx context.Context, count int) (int, error) {
	if count < 1 {
		return 0, errors.New("il numero di migrazioni da annullare dev'essere almeno 1")
	}
	if err := m.EnsureTable(ctx); err != nil {
		return 0, err
	}
	records, err := m.Applied(ctx)
	if err != nil {
		return 0, err
	}
	if err := m.Verify(records); err != nil {
		return 0, err
	}
	if len(records) == 0 {
		fmt.Fprintln(m.out, "nessuna migrazione applicata: niente da annullare")
		return 0, nil
	}

	byVersion := make(map[int]Migration, len(m.migrations))
	for _, mig := range m.migrations {
		byVersion[mig.Version] = mig
	}

	// Verify garantisce che ogni record abbia il suo file, quindi qui la
	// ricerca non può fallire.
	targets := records[max(0, len(records)-count):]
	slices.Reverse(targets)

	for i, record := range targets {
		if err := m.revert(ctx, byVersion[record.Version]); err != nil {
			return i, err
		}
	}
	fmt.Fprintln(m.out, pluralize(len(targets), "migrazione annullata", "migrazioni annullate"))
	return len(targets), nil
}

func (m *Migrator) revert(ctx context.Context, mig Migration) error {
	tx, err := m.conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("%s: apertura della transazione: %w", mig, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	start := time.Now()
	if _, err := tx.Exec(ctx, mig.Down); err != nil {
		return fmt.Errorf("%s: rollback: %w", mig, err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM `+TableName+` WHERE version = $1`, mig.Version); err != nil {
		return fmt.Errorf("%s: rimozione da %s: %w", mig, TableName, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("%s: commit: %w", mig, err)
	}

	fmt.Fprintf(m.out, "  ↓ %s (%s)\n", mig, time.Since(start).Round(time.Millisecond))
	return nil
}

// Version restituisce la versione più alta applicata, 0 se nessuna.
func (m *Migrator) Version(ctx context.Context) (int, error) {
	if err := m.EnsureTable(ctx); err != nil {
		return 0, err
	}
	records, err := m.Applied(ctx)
	if err != nil {
		return 0, err
	}
	version := 0
	for _, record := range records {
		version = max(version, record.Version)
	}
	return version, nil
}

// Status scrive su out l'elenco delle migrazioni con il loro stato.
func (m *Migrator) Status(ctx context.Context) error {
	if err := m.EnsureTable(ctx); err != nil {
		return err
	}
	records, err := m.Applied(ctx)
	if err != nil {
		return err
	}
	applied := make(map[int]Record, len(records))
	for _, record := range records {
		applied[record.Version] = record
	}

	// L'anomalia si segnala, ma non impedisce di vedere lo stato: `status` è il
	// comando che si lancia proprio quando qualcosa non torna.
	verifyErr := m.Verify(records)

	table := tabwriter.NewWriter(m.out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(table, "VERSIONE\tNOME\tSTATO\tAPPLICATA IL")
	for _, mig := range m.migrations {
		if record, ok := applied[mig.Version]; ok {
			state := "applicata"
			if record.Checksum != mig.Checksum {
				state = "MODIFICATA"
			}
			fmt.Fprintf(table, "%04d\t%s\t%s\t%s\n",
				mig.Version, mig.Name, state, record.AppliedAt.UTC().Format(time.RFC3339))
			continue
		}
		fmt.Fprintf(table, "%04d\t%s\t%s\t-\n", mig.Version, mig.Name, "pendente")
	}
	// Le migrazioni registrate ma senza file non comparirebbero nel ciclo sopra,
	// che scorre i file: sono proprio quelle che si vuole vedere.
	for _, record := range records {
		if !hasVersion(m.migrations, record.Version) {
			fmt.Fprintf(table, "%04d\t%s\t%s\t%s\n",
				record.Version, record.Name, "FILE ASSENTE", record.AppliedAt.UTC().Format(time.RFC3339))
		}
	}
	if err := table.Flush(); err != nil {
		return err
	}
	return verifyErr
}

func hasVersion(migrations []Migration, version int) bool {
	return slices.ContainsFunc(migrations, func(m Migration) bool { return m.Version == version })
}
