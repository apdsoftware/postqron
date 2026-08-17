package netguard

import (
	"context"
	"log/slog"
	"net"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

// Valori predefiniti dei tetti. Sono limiti del servizio e non preferenze del
// job (R40): un job non li alza.
const (
	// DefaultLookupTimeout limita la risoluzione DNS. Un nome il cui server
	// autoritativo non risponde non deve tenere occupata una richiesta all'API.
	DefaultLookupTimeout = 3 * time.Second
	// DefaultDialTimeout limita l'apertura della connessione TCP.
	DefaultDialTimeout = 10 * time.Second
	// DefaultMaxRedirects è il numero massimo di salti seguiti (R40).
	DefaultMaxRedirects = 5
)

// Resolver è la risoluzione dei nomi. `*net.Resolver` la soddisfa.
//
// È un'interfaccia per una ragione sola, e non è l'astrazione fine a sé stessa:
// **senza, il DNS rebinding non è verificabile**. La prova che il controllo e la
// connessione vedono lo stesso indirizzo richiede un risolutore che risponda in
// modo diverso alla prima e alla seconda domanda, e quello non si ottiene dal
// DNS vero.
type Resolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// Options sono le dipendenze e i tetti di un [Guard].
//
// Il valore zero produce un guard corretto e completo: policy predefinita,
// risolutore di sistema, tetti predefiniti. È voluto — un blocco di sicurezza
// che va configurato per essere sicuro è un blocco che qualcuno dimenticherà di
// configurare.
type Options struct {
	// Policy è l'elenco dei rifiuti. Il valore zero blocca già tutto il
	// necessario: si valorizza solo per aggiungere [Policy.Deny].
	Policy Policy
	// Resolver risolve i nomi. Nil significa il risolutore di sistema.
	Resolver Resolver
	// Logger riceve le ragioni dei rifiuti, che non vanno all'utente. Nil
	// significa nessun log.
	Logger *slog.Logger
	// LookupTimeout, DialTimeout e MaxRedirects: zero significa il predefinito.
	LookupTimeout time.Duration
	DialTimeout   time.Duration
	MaxRedirects  int
}

// Guard applica R38: alla validazione di un job e, soprattutto, a ogni
// connessione in uscita.
type Guard struct {
	policy        Policy
	resolver      Resolver
	log           *slog.Logger
	lookupTimeout time.Duration
	dialTimeout   time.Duration
	maxRedirects  int
}

// New costruisce il guard. `netguard.New(netguard.Options{})` è già la
// configurazione giusta per la produzione.
func New(opts Options) *Guard {
	g := &Guard{
		policy:        clonePolicy(opts.Policy),
		resolver:      opts.Resolver,
		log:           opts.Logger,
		lookupTimeout: opts.LookupTimeout,
		dialTimeout:   opts.DialTimeout,
		maxRedirects:  opts.MaxRedirects,
	}
	if g.resolver == nil {
		g.resolver = net.DefaultResolver
	}
	if g.log == nil {
		g.log = slog.New(slog.DiscardHandler)
	}
	if g.lookupTimeout <= 0 {
		g.lookupTimeout = DefaultLookupTimeout
	}
	if g.dialTimeout <= 0 {
		g.dialTimeout = DefaultDialTimeout
	}
	if g.maxRedirects <= 0 {
		g.maxRedirects = DefaultMaxRedirects
	}
	return g
}

// CheckTarget rifiuta un URL alla validazione del job. Soddisfa
// `jobs.TargetGuard`.
//
// # Questo non è il blocco
//
// È l'anticipo del blocco, e serve a due cose oneste: dire subito all'utente che
// quell'URL non funzionerà, invece di lasciargli scoprire alle tre di notte un
// job che fallisce da settimane; e togliere dal database i target che sappiamo
// già di non voler chiamare. **Non è una difesa**, e non può esserlo: fra
// questo controllo e la richiesta vera passano ore, e chi controlla il DNS del
// nome può cambiare la risposta in mezzo. La difesa è [Guard.DialContext].
//
// # Perché una risoluzione fallita non è un rifiuto
//
// Un nome che oggi non risolve non è un nome interno: è un nome che non
// risolve. Può essere un dominio appena registrato, un DNS momentaneamente
// irraggiungibile, o il nostro risolutore che ha avuto un brutto secondo.
// Rifiutare qui significherebbe far fallire la modifica di un job per un guasto
// che non riguarda l'utente — e non guadagnerebbe niente in sicurezza, perché
// quel nome verrebbe comunque ricontrollato, e bloccato, al momento della
// connessione. In più muddia l'oracolo: «accettato» significa «pubblico oppure
// non risolvibile», non «pubblico». Vedi [ErrNotAllowed].
func (g *Guard) CheckTarget(ctx context.Context, target *url.URL) error {
	if target == nil {
		return ErrNotAllowed
	}
	scheme := strings.ToLower(target.Scheme)
	if scheme != "http" && scheme != "https" {
		// Il chiamante lo controlla già (`jobs_url_scheme_check` e
		// jobs.validateURL). Ricontrollarlo qui costa una riga e vale nel caso in
		// cui il guard venga usato da un chiamante che non lo fa.
		g.refuse(ctx, "check", target.Host, netip.Addr{}, "schema non ammesso: "+target.Scheme)
		return ErrNotAllowed
	}

	// Hostname() toglie le parentesi quadre di un letterale IPv6 e — cosa che
	// conta di più — ignora la parte `utente:password@`. È lì che vive il
	// travestimento `http://api.example.com@127.0.0.1/`, che a occhio umano è
	// un URL verso example.com e per qualunque client HTTP è un URL verso
	// 127.0.0.1.
	host := target.Hostname()
	if host == "" {
		g.refuse(ctx, "check", target.Host, netip.Addr{}, "URL senza host")
		return ErrNotAllowed
	}

	// Un letterale non si risolve: si guarda. Vale anche per le forme che il
	// risolutore di sistema accetterebbe ma net/netip no (`0177.0.0.1`,
	// `2130706433`): quelle cadono nel ramo sotto e vengono giudicate su ciò
	// che il risolutore risponde, che è la sola forma canonica che conti.
	if addr, err := netip.ParseAddr(host); err == nil {
		if ok, reason := g.policy.Allows(addr); !ok {
			g.refuse(ctx, "check", host, addr, reason)
			return ErrNotAllowed
		}
		return nil
	}

	addrs, err := g.lookup(ctx, "ip", host)
	if err != nil || len(addrs) == 0 {
		g.log.DebugContext(ctx, "netguard: nome non risolto alla validazione, controllo rimandato alla connessione",
			slog.String("host", host), slog.Any("error", err))
		return nil
	}
	if addr, reason, ok := g.firstRefused(addrs); !ok {
		g.refuse(ctx, "check", host, addr, reason)
		return ErrNotAllowed
	}
	return nil
}

// firstRefused restituisce il primo indirizzo non ammesso dell'insieme.
//
// # Perché basta uno
//
// Se un nome risolve a un indirizzo pubblico *e* a uno interno, il nome è
// rifiutato: non si tiene la parte buona. Chi controlla la zona DNS decide
// quale record il risolutore restituisce per primo, e quale ordine il client
// userà al tentativo successivo — «prendo solo quelli buoni» significa aver
// accettato che il bersaglio è raggiungibile e aver scelto di non andarci
// adesso. La regola è: un nome che *può* portare all'interno non è una
// destinazione.
func (g *Guard) firstRefused(addrs []netip.Addr) (netip.Addr, string, bool) {
	for _, addr := range addrs {
		if ok, reason := g.policy.Allows(addr); !ok {
			return addr, reason, false
		}
	}
	return netip.Addr{}, "", true
}

// lookup risolve con il tetto di tempo del guard.
func (g *Guard) lookup(ctx context.Context, network, host string) ([]netip.Addr, error) {
	ctx, cancel := context.WithTimeout(ctx, g.lookupTimeout)
	defer cancel()
	return g.resolver.LookupNetIP(ctx, network, host)
}

// refuse scrive la ragione dove può stare: nel log dell'operatore.
//
// Il messaggio all'utente resta [ErrNotAllowed], sempre lo stesso. Qui invece
// serve il dettaglio: senza, un rifiuto legittimo che l'utente contesta non è
// diagnosticabile da nessuno, e un tentativo di scansione non è distinguibile da
// un errore di battitura.
func (g *Guard) refuse(ctx context.Context, stage, host string, addr netip.Addr, reason string) {
	attrs := []any{
		slog.String("stage", stage),
		slog.String("host", host),
		slog.String("reason", reason),
	}
	if addr.IsValid() {
		attrs = append(attrs, slog.String("addr", addr.String()))
	}
	g.log.WarnContext(ctx, "netguard: destinazione rifiutata (R38)", attrs...)
}
