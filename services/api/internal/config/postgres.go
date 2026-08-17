package config

import (
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"slices"
	"strconv"
	"strings"
)

// Postgres descrive la connessione al database.
//
// Le variabili d'ambiente `POSTGRES_*` sono l'**unica fonte di verità**: non
// esiste, e non va introdotta, una `DATABASE_URL` già formata. Un DSN
// precomposto duplicherebbe host, porta e credenziali in un secondo posto,
// libero di sfasarsi dal primo in silenzio (AGENTS.md §7). Il DSN si compone
// qui, da queste variabili, ed è l'unico punto in cui esiste.
type Postgres struct {
	// Host è un nome, un indirizzo IP oppure — nella forma usata da libpq — la
	// directory di un socket Unix, riconosciuta dallo slash iniziale
	// (per esempio /var/run/postgresql).
	Host string
	// Port resta significativa anche sul socket Unix: libpq la usa per comporre
	// il nome del file (.s.PGSQL.5432).
	Port     string
	Database string
	User     string
	Password string
	SSLMode  string
}

// Modalità TLS accettate da libpq.
var validSSLModes = []string{"disable", "allow", "prefer", "require", "verify-ca", "verify-full"}

// Modalità che garantiscono la cifratura del canale: se il server non offre TLS,
// la connessione fallisce invece di proseguire in chiaro.
//
// `allow` e `prefer` non sono qui, pur negoziando TLS: entrambe ripiegano sul
// testo in chiaro **senza segnalarlo** quando il server non lo offre, quindi
// lasciano aperto lo stesso buco di `disable`, solo condizionato al comportamento
// del server. Un fallback silenzioso è peggio di un rifiuto esplicito.
//
// Fra le tre, `verify-full` è preferibile: è l'unica che verifica anche l'identità
// del server, non solo la cifratura. Non è però obbligatoria, perché con certi
// provider gestiti `require` può essere l'unica opzione praticabile.
var encryptedSSLModes = []string{"require", "verify-ca", "verify-full"}

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

	// In produzione API e PostgreSQL stanno sulla stessa VPS, scelta esplicita
	// per la latenza (SPEC §2): lì una connessione in chiaro sul loopback o su
	// socket Unix non attraversa alcuna rete, e vietarla romperebbe
	// l'architettura. Il rischio da coprire è un altro — credenziali e dati in
	// chiaro *fuori* dalla macchina — quindi il vincolo è sull'abbinamento fra
	// sslmode e host, non sul valore di sslmode da solo.
	//
	// Un host remoto in produzione è già fuori dall'ordinario, data l'architettura:
	// non esiste un caso legittimo che giustifichi un fallback opportunistico.
	if production && !p.isLocal() && !slices.Contains(encryptedSSLModes, p.SSLMode) {
		return p.remoteSSLModeError()
	}
	return nil
}

// remoteSSLModeError spiega perché la modalità richiesta non basta su un host
// remoto. La conseguenza cambia col valore: `disable` non cifra affatto, mentre
// `allow` e `prefer` cifrano solo se il server collabora — e tacciono quando non
// lo fa. Dirlo esplicitamente è ciò che rende l'errore azionabile.
func (p Postgres) remoteSSLModeError() error {
	consequence := "la connessione non sarebbe cifrata, quindi credenziali e dati " +
		"viaggerebbero in chiaro sulla rete"
	if p.SSLMode == "allow" || p.SSLMode == "prefer" {
		consequence = "questa modalità negozia TLS ma ripiega sul testo in chiaro " +
			"senza segnalarlo, se il server non lo offre: credenziali e dati potrebbero " +
			"quindi viaggiare in chiaro sulla rete senza che nulla lo indichi"
	}
	return fmt.Errorf(
		"POSTGRES_SSLMODE=%q non è ammesso in produzione con POSTGRES_HOST=%q, che è fuori "+
			"dalla macchina: %s. Usa %s — verify-full è preferibile, è l'unica che verifica "+
			"anche l'identità del server, ma con alcuni provider gestiti require può essere "+
			"l'unica praticabile. Su host locale (localhost, 127.0.0.1, ::1 o un percorso di "+
			"socket Unix) disable resta ammesso",
		p.SSLMode, p.Host, consequence, orList(encryptedSSLModes))
}

// orList formatta un elenco per un messaggio in italiano: "a, b o c".
func orList(items []string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	default:
		return strings.Join(items[:len(items)-1], ", ") + " o " + items[len(items)-1]
	}
}

// isLocal indica se la connessione resta dentro la macchina.
//
// Limite noto e deliberato: un nome che *risolve* al loopback (un `db.local` che
// punta a 127.0.0.1) qui risulta remoto. Risolvere il DNS in fase di validazione
// significherebbe far dipendere l'avvio del processo dal resolver e fidarsi di
// una risposta che può cambiare subito dopo il controllo: meglio un falso
// allarme, che si mette a tacere scrivendo l'indirizzo, di un via libera basato
// su un dato volatile.
func (p Postgres) isLocal() bool {
	if isUnixSocket(p.Host) {
		return true
	}
	if p.Host == "localhost" {
		return true
	}
	// Copre 127.0.0.0/8 e ::1.
	if ip := net.ParseIP(p.Host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

// isUnixSocket riconosce un host indicato come directory del socket, nella forma
// usata da libpq (per esempio /var/run/postgresql).
func isUnixSocket(host string) bool { return strings.HasPrefix(host, "/") }

// DSN compone la stringa di connessione a PostgreSQL.
//
// Attenzione: il valore restituito **contiene la password in chiaro**. Va
// passato solo al driver del database, mai loggato e mai restituito dall'API
// (SPEC §5). Per i log usare direttamente il valore Postgres, che si redige da
// sé tramite LogValue.
func (p Postgres) DSN() string {
	query := url.Values{"sslmode": {p.SSLMode}}

	u := &url.URL{
		Scheme: "postgres",
		Path:   "/" + p.Database,
	}
	if isUnixSocket(p.Host) {
		// Per un socket Unix libpq vuole l'authority vuota e la directory nel
		// parametro `host`: metterla nell'authority produrrebbe un URI in cui gli
		// slash del percorso sono separatori.
		query.Set("host", p.Host)
		query.Set("port", p.Port)
	} else {
		u.Host = net.JoinHostPort(p.Host, p.Port)
	}
	u.RawQuery = query.Encode()

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
