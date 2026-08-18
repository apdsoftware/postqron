package health

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// probeSQL chiede al database tutto quello che serve, in un round trip solo.
//
// Le tre colonne rispondono a tre domande diverse e sono nella stessa query per
// una ragione di costo: su una macchina sola (SPEC §2) il round trip vale più
// del lavoro che fa, e tre query separate costerebbero tre volte tanto per la
// stessa risposta.
//
//  1. `pg_is_in_recovery()` distingue un database che **legge** da uno che
//     **scrive**. Una replica in sola lettura risponde perfettamente a
//     `SELECT 1`, e con quello come sonda il servizio si dichiarerebbe sano
//     mentre nessuna esecuzione può essere registrata.
//  2. L'ultimo giorno coperto da una partizione di `job_executions`. Il nome
//     porta la data (`job_executions_20260817`, migrazione 0006) ed è da lì che
//     si legge: è la stessa forma su cui la 0006 costruisce il proprio DROP, e
//     leggere il catalogo costa una scansione di poche decine di righe — una
//     partizione al giorno per la finestra di retention, non una per job.
//  3. La data di oggi **secondo il database**, non secondo il processo. Il
//     confronto dev'essere fra due orologi coerenti: le partizioni sono create
//     dal `now()` del database (0006), e usare quello del processo
//     introdurrebbe uno scarto proprio nella grandezza che serve a decidere se
//     manca la partizione di oggi.
//
// Non c'è nessuna scrittura di prova, ed è deliberato: scrivere una riga a ogni
// passata per dimostrare di saper scrivere significa aggiungere traffico
// all'unica tabella il cui costo è dominato dal volume. `pg_is_in_recovery()`
// copre il caso che conta — il database che ha smesso di accettare scritture —
// e ciò che resta fuori, un disco pieno, si manifesta comunque come esecuzioni
// che falliscono e come sonda che non risponde entro il proprio tetto.
const probeSQL = `
	SELECT pg_is_in_recovery(),
	       (SELECT max(to_date(right(c.relname, 8), 'YYYYMMDD'))
	          FROM pg_class c
	          JOIN pg_inherits i ON i.inhrelid = c.oid
	         WHERE i.inhparent = 'job_executions'::regclass
	           AND c.relname ~ '^job_executions_[0-9]{8}$'),
	       (now() AT TIME ZONE 'UTC')::date`

// probeState è ciò che il database ha risposto.
type probeState struct {
	// inRecovery dice che il database non accetta scritture.
	inRecovery bool
	// hasPartitions è falso quando `job_executions` non ha nessuna partizione.
	hasPartitions bool
	// lastDay è l'ultimo giorno coperto, horizonDays quanti ne restano da oggi.
	// Zero significa «la partizione di oggi c'è ed è l'ultima»: il motore
	// scrive ancora, e domani non più.
	lastDay     time.Time
	horizonDays int
}

func (s *Service) probe(ctx context.Context) (probeState, error) {
	var (
		state probeState
		last  *time.Time
		today time.Time
	)
	err := s.pool.QueryRow(ctx, probeSQL).Scan(&state.inRecovery, &last, &today)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Impossibile per costruzione — la query restituisce sempre una riga
			// — ma dirlo è meglio che restituire uno stato a zero che si
			// leggerebbe come «nessuna partizione».
			return probeState{}, errors.New("il database non ha risposto alla sonda")
		}
		return probeState{}, err
	}
	if last == nil {
		// Nessuna partizione: o la 0006 non è stata applicata, o le ha portate
		// via tutte la retention. In entrambi i casi il motore non può scrivere.
		return state, nil
	}
	state.hasPartitions = true
	state.lastDay = *last
	// Le due date sono a mezzanotte UTC per costruzione: la differenza in giorni
	// è esatta e non dipende dal fuso di nessuno.
	state.horizonDays = int(last.Sub(today).Hours() / 24)
	return state, nil
}
