package config

import (
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"slices"
	"strconv"
)

// Postgres descrive la connessione al database.
//
// Le variabili d'ambiente `POSTGRES_*` sono l'**unica fonte di verità**: non
// esiste, e non va introdotta, una `DATABASE_URL` già formata. Un DSN
// precomposto duplicherebbe host, porta e credenziali in un secondo posto,
// libero di sfasarsi dal primo in silenzio (AGENTS.md §7). Il DSN si compone
// qui, da queste variabili, ed è l'unico punto in cui esiste.
type Postgres struct {
	Host     string
	Port     string
	Database string
	User     string
	Password string
	SSLMode  string
}

// Modalità TLS accettate da libpq.
var validSSLModes = []string{"disable", "allow", "prefer", "require", "verify-ca", "verify-full"}

// loadPostgres legge le variabili POSTGRES_*.
//
// I default valgono **solo fuori dalla produzione** e coincidono con quelli di
// `.env.example`. In produzione non se ne applica nessuno: un deploy che
// dimentica `POSTGRES_HOST` deve fallire all'avvio, non collegarsi in silenzio
// al localhost della VPS.
func loadPostgres(getenv Getenv, production bool) Postgres {
	if production {
		return Postgres{
			Host:     stringOr(getenv, "POSTGRES_HOST", ""),
			Port:     stringOr(getenv, "POSTGRES_PORT", ""),
			Database: stringOr(getenv, "POSTGRES_DB", ""),
			User:     stringOr(getenv, "POSTGRES_USER", ""),
			Password: getenv("POSTGRES_PASSWORD"),
			SSLMode:  stringOr(getenv, "POSTGRES_SSLMODE", ""),
		}
	}
	return Postgres{
		Host:     stringOr(getenv, "POSTGRES_HOST", "127.0.0.1"),
		Port:     stringOr(getenv, "POSTGRES_PORT", "5432"),
		Database: stringOr(getenv, "POSTGRES_DB", "postqron"),
		User:     stringOr(getenv, "POSTGRES_USER", "postqron"),
		// La password non ha default: un valore in chiaro nel codice sarebbe una
		// credenziale versionata. In sviluppo arriva da `.env`, in produzione è
		// obbligatoria.
		Password: getenv("POSTGRES_PASSWORD"),
		SSLMode:  stringOr(getenv, "POSTGRES_SSLMODE", "disable"),
	}
}

// validate controlla la connessione. In produzione ogni variabile è
// obbligatoria: meglio fallire all'avvio che scoprirlo al primo dispatch.
func (p Postgres) validate(production bool) error {
	if production {
		required := []struct{ name, value string }{
			{"POSTGRES_HOST", p.Host},
			{"POSTGRES_PORT", p.Port},
			{"POSTGRES_DB", p.Database},
			{"POSTGRES_USER", p.User},
			{"POSTGRES_PASSWORD", p.Password},
			{"POSTGRES_SSLMODE", p.SSLMode},
		}
		for _, r := range required {
			if r.value == "" {
				return fmt.Errorf("%s è obbligatoria in produzione", r.name)
			}
		}
	}

	// Una porta non numerica produrrebbe un errore di connessione oscuro molto
	// più tardi: qui costa una riga.
	port, err := strconv.Atoi(p.Port)
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("POSTGRES_PORT non è una porta valida: %q", p.Port)
	}
	if !slices.Contains(validSSLModes, p.SSLMode) {
		return fmt.Errorf("POSTGRES_SSLMODE non valido: %q (ammessi: %v)", p.SSLMode, validSSLModes)
	}
	return nil
}

// DSN compone la stringa di connessione a PostgreSQL.
//
// Attenzione: il valore restituito **contiene la password in chiaro**. Va
// passato solo al driver del database, mai loggato e mai restituito dall'API
// (SPEC §5). Per i log usare direttamente il valore Postgres, che si redige da
// sé tramite LogValue.
func (p Postgres) DSN() string {
	u := &url.URL{
		Scheme:   "postgres",
		Host:     net.JoinHostPort(p.Host, p.Port),
		Path:     "/" + p.Database,
		RawQuery: url.Values{"sslmode": {p.SSLMode}}.Encode(),
	}
	// url.UserPassword con password vuota emetterebbe un "utente:" pendente.
	if p.Password == "" {
		u.User = url.User(p.User)
	} else {
		u.User = url.UserPassword(p.User, p.Password)
	}
	return u.String()
}

// LogValue implementa slog.LogValuer: loggare un Postgres non può far uscire la
// password, nemmeno per distrazione.
func (p Postgres) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("host", p.Host),
		slog.String("port", p.Port),
		slog.String("database", p.Database),
		slog.String("user", p.User),
		slog.String("sslmode", p.SSLMode),
	)
}
