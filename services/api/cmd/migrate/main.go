// Command migrate applica e annulla le migrazioni versionate di Postqron.
//
//	migrate up [n]      applica le migrazioni pendenti, tutte oppure le prime n
//	migrate down [n]    annulla le ultime migrazioni applicate, di default una
//	migrate status      elenca le migrazioni con il loro stato
//	migrate version     stampa la versione dello schema
//
// La connessione arriva dalle variabili POSTGRES_*, l'unica fonte di verità del
// progetto (AGENTS.md §7): il DSN è composto da config.Postgres, non qui.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/apdsoftware/postqron/services/api/internal/config"
	"github.com/apdsoftware/postqron/services/api/internal/database"
	"github.com/apdsoftware/postqron/services/api/internal/dotenv"
	"github.com/apdsoftware/postqron/services/api/internal/migrate"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "errore: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("migrate", flag.ContinueOnError)
	flags.SetOutput(stderr)
	dir := flags.String("dir", "", "directory delle migrazioni (default: db/migrations, cercata risalendo)")
	flags.Usage = func() { usage(stderr) }
	if err := flags.Parse(args); err != nil {
		// `migrate -h` ha già stampato l'uso: non è un errore da ripetere.
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	command := "up"
	if flags.NArg() > 0 {
		command = flags.Arg(0)
	}

	count, err := parseCount(command, flags.Args())
	if err != nil {
		return err
	}

	// In sviluppo le POSTGRES_* stanno nel `.env` che Docker Compose ha già
	// usato per creare il container: senza rileggerlo qui, il tool cercherebbe
	// il database su valori di default che nessuno ha scelto. Riempie solo ciò
	// che manca — in produzione il file non esiste — e le divergenze sulla
	// connessione fermano il comando invece di risolversi in silenzio.
	workdir, err := os.Getwd()
	if err != nil {
		return err
	}
	conflicts, err := dotenv.LoadNearest(workdir)
	if err != nil {
		return err
	}
	if err := refuseConnectionConflicts(conflicts); err != nil {
		return err
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	migrationsDir := *dir
	if migrationsDir == "" {
		if migrationsDir, err = migrate.FindDir(workdir); err != nil {
			return err
		}
	}
	migrations, err := migrate.Load(migrationsDir)
	if err != nil {
		return err
	}
	if len(migrations) == 0 {
		return fmt.Errorf("nessuna migrazione trovata in %s", migrationsDir)
	}

	// Il contesto si chiude al primo segnale: una migrazione interrotta rotola
	// indietro con la sua transazione, e il lock consultivo cade con la sessione.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Una sola connessione: il lock consultivo che serializza i migratori è
	// legato alla sessione, e da una seconda connessione del pool non sarebbe
	// nemmeno visibile.
	pool, err := database.Open(ctx, cfg.Postgres, database.Options{
		MaxConns:        1,
		ApplicationName: "postqron-migrate",
	})
	if err != nil {
		return err
	}
	defer pool.Close()

	conn, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()

	fmt.Fprintf(stdout, "database %s@%s:%s/%s · migrazioni da %s\n",
		cfg.Postgres.User, cfg.Postgres.Host, cfg.Postgres.Port, cfg.Postgres.Database, migrationsDir)

	migrator := migrate.New(conn, migrations, stdout)

	return migrator.WithLock(ctx, func(ctx context.Context) error {
		switch command {
		case "up":
			_, err := migrator.Up(ctx, count)
			return err
		case "down":
			_, err := migrator.Down(ctx, count)
			return err
		case "status":
			return migrator.Status(ctx)
		case "version":
			version, err := migrator.Version(ctx)
			if err != nil {
				return err
			}
			fmt.Fprintf(stdout, "versione dello schema: %04d\n", version)
			return nil
		default:
			usage(stderr)
			return fmt.Errorf("comando sconosciuto: %q", command)
		}
	})
}

// refuseConnectionConflicts ferma il tool quando ambiente e `.env` descrivono
// due database diversi.
//
// Il motivo non è di gusto. `make migrate` esegue prima `scripts/db-guard.sh`,
// che verifica che su `POSTGRES_HOST:POSTGRES_PORT` risponda il container di
// Postqron e non un altro PostgreSQL in ascolto sulla stessa porta. Quello
// script legge le variabili con `set -a; . ./.env`, cioè dando la precedenza al
// **file**; questo tool la dà all'**ambiente**. Finché i due valori coincidono
// la differenza è invisibile, ma un `POSTGRES_PORT=15432 make migrate` farebbe
// controllare alla guardia la porta del `.env` e connettere il tool a un'altra:
// la verifica passerebbe su un server e la migrazione andrebbe su un altro.
//
// Rifiutare è preferibile sia al vincere dell'ambiente — che aggira in silenzio
// una guardia esistente — sia al vincere del file, che ignorerebbe senza dirlo
// una variabile impostata di proposito. La via d'uscita è quella che il
// progetto indica ovunque: cambiare il valore in `.env`, che è l'unico posto in
// cui la connessione è descritta (AGENTS.md §7).
//
// Il controllo riguarda le sole `POSTGRES_*`: sono quelle che decidono su quale
// database si sta per scrivere. Una `POSTQRON_LOG_LEVEL` divergente non fa
// danni e non merita un blocco.
func refuseConnectionConflicts(conflicts []dotenv.Conflict) error {
	var relevant []dotenv.Conflict
	for _, conflict := range conflicts {
		if strings.HasPrefix(conflict.Key, "POSTGRES_") {
			relevant = append(relevant, conflict)
		}
	}
	if len(relevant) == 0 {
		return nil
	}

	var detail strings.Builder
	for _, conflict := range relevant {
		fmt.Fprintf(&detail, "\n  %s", conflict)
	}
	return fmt.Errorf(
		"l'ambiente e il .env descrivono connessioni diverse:%s\n"+
			"`make migrate` verifica l'identità del container leggendo il .env, ma il tool "+
			"userebbe l'ambiente: la guardia controllerebbe un server e la migrazione ne "+
			"toccherebbe un altro. Cambia il valore in .env — è l'unico posto in cui la "+
			"connessione è descritta — oppure togli la variabile dall'ambiente",
		detail.String())
}

// parseCount legge l'argomento numerico opzionale di `up` e `down`.
//
// Per `up` l'assenza significa «tutte»; per `down` significa una sola. Il
// default asimmetrico è voluto: applicare tutto è la normalità, annullare tutto
// non deve poter succedere per distrazione.
func parseCount(command string, args []string) (int, error) {
	if command != "up" && command != "down" {
		if len(args) > 1 {
			return 0, fmt.Errorf("%s non accetta argomenti", command)
		}
		return 0, nil
	}
	if len(args) < 2 {
		if command == "down" {
			return 1, nil
		}
		return 0, nil
	}
	if len(args) > 2 {
		return 0, fmt.Errorf("%s accetta al massimo un argomento", command)
	}
	count, err := strconv.Atoi(args[1])
	if err != nil || count < 1 {
		return 0, fmt.Errorf("%s: il numero di migrazioni dev'essere un intero positivo, non %q", command, args[1])
	}
	return count, nil
}

func usage(w io.Writer) {
	fmt.Fprint(w, `Uso: migrate [-dir percorso] <comando> [n]

Comandi:
  up [n]      applica le migrazioni pendenti; con n, solo le prime n
  down [n]    annulla le ultime n migrazioni applicate (default 1)
  status      elenca le migrazioni con il loro stato
  version     stampa la versione dello schema

La connessione si configura con le variabili POSTGRES_HOST, POSTGRES_PORT,
POSTGRES_DB, POSTGRES_USER, POSTGRES_PASSWORD e POSTGRES_SSLMODE, lette anche
dal file .env più vicino. POSTQRON_MIGRATIONS_DIR sostituisce -dir.
`)
}
