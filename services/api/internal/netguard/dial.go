package netguard

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// DialContext è il blocco vero: apre la connessione solo verso un indirizzo
// ammesso, e verso **quell'indirizzo**.
//
// # La sequenza, e perché è in quest'ordine
//
//  1. Si separa host e porta di ciò che il trasporto chiede.
//  2. Si risolve il nome **una volta sola**, qui.
//  3. Si controlla *ogni* indirizzo ottenuto: se anche uno solo non è ammesso,
//     non si esce affatto (vedi [Guard.firstRefused]).
//  4. Si dialoga con l'indirizzo **letterale**, non con il nome.
//  5. Subito prima della `connect(2)`, [Guard.control] riguarda l'indirizzo che
//     il socket sta davvero per contattare.
//
// Il passo 4 è il punto in cui si vince o si perde contro il DNS rebinding. La
// forma sbagliata — quella che quasi tutte le implementazioni hanno — è
// risolvere, validare, e poi passare *il nome* al dialer: il dialer risolve di
// nuovo, e la seconda risposta può essere 127.0.0.1. Fra il nostro controllo e
// la nostra `connect` non c'è nessuna seconda risoluzione: non c'è la finestra.
//
// Il passo 5 sembra ridondante rispetto al 3, e in questo codice lo è. Esiste
// perché è l'unico controllo che non guarda ciò che *crediamo* di aver
// costruito ma ciò che il kernel sta per fare, ed è quello che continuerà a
// valere il giorno in cui qualcuno cambierà i passi 2–4.
//
// # Cosa lo rende inevitabile
//
// Il controllo non sta in `CheckRedirect` — sta qui, sotto il client HTTP. Ogni
// salto della catena di redirect apre una connessione, e ogni connessione passa
// da questa funzione: non c'è un hop che possa saltarla, e non ci sarà nemmeno
// per un hop che aggiungessimo domani. Un controllo scritto in `CheckRedirect`
// avrebbe la forma giusta e la garanzia sbagliata.
func (g *Guard) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("netguard: indirizzo %q illeggibile: %w", address, err)
	}
	port, err := strconv.ParseUint(portText, 10, 16)
	if err != nil {
		return nil, fmt.Errorf("netguard: porta %q non valida", portText)
	}

	addrs, err := g.resolveForDial(ctx, network, host)
	if err != nil {
		// La risoluzione fallita in esecuzione è un guasto della destinazione,
		// non un rifiuto nostro: va detta com'è, perché finisce nel registro
		// delle esecuzioni dove all'utente serve capire che il suo nome non
		// esiste più. Non aggiunge un oracolo: è la stessa risposta che
		// otterrebbe interrogando il DNS da casa sua.
		return nil, err
	}
	if len(addrs) == 0 {
		return nil, fmt.Errorf("netguard: %q non ha indirizzi", host)
	}

	if addr, reason, ok := g.firstRefused(addrs); !ok {
		g.refuse(ctx, "dial", host, addr, reason)
		return nil, ErrNotAllowed
	}

	dialer := &net.Dialer{Timeout: g.dialTimeout, ControlContext: g.control}
	var lastErr error
	for _, addr := range addrs {
		// AddrPortFrom + String() produce la forma letterale corretta per
		// entrambe le famiglie (`93.184.215.14:443`, `[2606:4700::1111]:443`).
		// È questa stringa, non `address`, che arriva al sistema.
		conn, err := dialer.DialContext(ctx, network, netip.AddrPortFrom(addr, uint16(port)).String())
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

// resolveForDial ottiene gli indirizzi candidati per una connessione.
//
// Un letterale resta sé stesso e non tocca il DNS. Un nome viene risolto qui, e
// **solo** qui: è l'unica risoluzione dell'intera connessione.
func (g *Guard) resolveForDial(ctx context.Context, network, host string) ([]netip.Addr, error) {
	if addr, err := netip.ParseAddr(host); err == nil {
		return []netip.Addr{addr}, nil
	}
	return g.lookup(ctx, lookupNetwork(network), host)
}

// lookupNetwork traduce la rete del dialer in quella del risolutore.
//
// Serve perché `tcp4` deve restare `tcp4`: risolvere in `ip` e poi tentare un
// indirizzo IPv6 su una connessione forzata a IPv4 produrrebbe un errore
// incomprensibile invece di una connessione.
func lookupNetwork(network string) string {
	switch network {
	case "tcp4", "udp4", "ip4":
		return "ip4"
	case "tcp6", "udp6", "ip6":
		return "ip6"
	default:
		return "ip"
	}
}

// control è l'ultimo controllo prima della `connect(2)`.
//
// Il parametro `address` è l'indirizzo che il socket sta per contattare, letto
// dopo che il sistema ha fatto tutto ciò che doveva fare. È il solo punto in cui
// «l'indirizzo che ho validato» e «l'indirizzo a cui mi sto connettendo» sono
// per costruzione la stessa cosa, invece di esserlo perché il codice sopra è
// scritto bene.
//
// Un indirizzo illeggibile è un rifiuto e non un'approvazione: se non sappiamo
// dove stiamo andando, non ci andiamo.
func (g *Guard) control(ctx context.Context, _, address string, _ syscall.RawConn) error {
	addrPort, err := netip.ParseAddrPort(address)
	if err != nil {
		g.refuse(ctx, "connect", address, netip.Addr{}, "indirizzo del socket illeggibile: "+err.Error())
		return ErrNotAllowed
	}
	if ok, reason := g.policy.Allows(addrPort.Addr()); !ok {
		g.refuse(ctx, "connect", address, addrPort.Addr(), reason)
		return ErrNotAllowed
	}
	return nil
}

// Transport è il trasporto HTTP che passa dal blocco.
//
// # Proxy: nil, e non è una dimenticanza
//
// `http.DefaultTransport` usa [http.ProxyFromEnvironment]. Con `HTTP_PROXY`
// impostata nell'ambiente del processo, il trasporto aprirebbe la connessione
// **verso il proxy** — indirizzo che il nostro dialer troverebbe perfettamente
// legittimo — e sarebbe il proxy, per conto nostro, a raggiungere
// 169.254.169.254. Il blocco sparirebbe per una variabile d'ambiente, che è
// esattamente l'aggiramento che la Acceptable Use Policy §2.2 vieta agli utenti
// e che non vogliamo poterci infliggere da soli. Postqron chiama i bersagli
// direttamente: se un giorno servisse un proxy in uscita, il controllo va
// spostato sul proxy, non tolto da qui.
func (g *Guard) Transport() *http.Transport {
	return &http.Transport{
		Proxy:       nil,
		DialContext: g.DialContext,
		// Il nome è ancora quello dell'URL, quindi SNI e verifica del
		// certificato continuano a funzionare: ci si connette all'indirizzo
		// pinnato, ma si parla TLS con l'host che l'utente ha scritto.
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
}

// Client è il client HTTP che chi esegue i job (#389) deve usare.
//
// Il timeout complessivo resta al chiamante: è per job (`timeout_seconds`,
// R40), non del guard, e si applica con un `context` sulla richiesta.
func (g *Guard) Client() *http.Client {
	return &http.Client{
		Transport:     g.Transport(),
		CheckRedirect: g.CheckRedirect,
	}
}

// CheckRedirect limita la catena di redirect e ne verifica lo schema.
//
// **Non controlla l'indirizzo**, di proposito. Controllarlo qui vorrebbe dire
// risolvere il nome del salto successivo per poi lasciare che il trasporto lo
// risolva di nuovo: la stessa finestra di rebinding che [Guard.DialContext]
// chiude, riaperta nel punto in cui sembra più naturale metterla. L'indirizzo lo
// guarda chi apre la connessione, che è l'unico a poterlo fare nel momento
// giusto.
//
// Quello che invece va guardato qui è lo schema: `Location: file:///etc/passwd`
// o `gopher://…` non sono destinazioni eseguibili (SPEC §10).
func (g *Guard) CheckRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= g.maxRedirects {
		return fmt.Errorf("%w: massimo %d", ErrTooManyRedirects, g.maxRedirects)
	}
	scheme := strings.ToLower(req.URL.Scheme)
	if scheme != "http" && scheme != "https" {
		g.refuse(req.Context(), "redirect", req.URL.Host, netip.Addr{}, "schema non ammesso nel redirect: "+req.URL.Scheme)
		return ErrNotAllowed
	}
	return nil
}
