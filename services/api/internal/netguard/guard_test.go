package netguard_test

import (
	"context"
	"errors"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/apdsoftware/postqron/services/api/internal/netguard"
)

// risolutoreFinto risponde alle domande DNS con ciò che decide il test.
//
// Non è un doppio di comodo: è l'unico modo di provare il DNS rebinding, che per
// definizione richiede due risposte diverse alla stessa domanda. Conta anche le
// chiamate, perché «quante volte è stato risolto» è a sua volta una proprietà da
// verificare: [netguard.Guard.DialContext] deve risolvere **una volta sola** per
// connessione, altrimenti fra il controllo e la connessione si riapre la
// finestra che il pacchetto esiste per chiudere.
type risolutoreFinto struct {
	rispondi func(chiamata int, host string) ([]netip.Addr, error)

	mu       sync.Mutex
	conteggi map[string]int
}

func nuovoRisolutore(rispondi func(chiamata int, host string) ([]netip.Addr, error)) *risolutoreFinto {
	return &risolutoreFinto{rispondi: rispondi, conteggi: map[string]int{}}
}

// fisso è il caso più comune: un nome, sempre gli stessi indirizzi.
func fisso(tabella map[string][]string) *risolutoreFinto {
	return nuovoRisolutore(func(_ int, host string) ([]netip.Addr, error) {
		testi, ok := tabella[host]
		if !ok {
			return nil, &erroreDNS{host: host}
		}
		return indirizzi(testi...), nil
	})
}

func (r *risolutoreFinto) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	r.mu.Lock()
	r.conteggi[host]++
	n := r.conteggi[host]
	r.mu.Unlock()
	return r.rispondi(n, host)
}

func (r *risolutoreFinto) chiamate(host string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.conteggi[host]
}

func (r *risolutoreFinto) totale() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, v := range r.conteggi {
		n += v
	}
	return n
}

// erroreDNS imita un nome che non esiste.
type erroreDNS struct{ host string }

func (e *erroreDNS) Error() string { return "lookup " + e.host + ": no such host" }

func indirizzi(testi ...string) []netip.Addr {
	out := make([]netip.Addr, 0, len(testi))
	for _, t := range testi {
		out = append(out, netip.MustParseAddr(t))
	}
	return out
}

func controlla(t *testing.T, g *netguard.Guard, raw string) error {
	t.Helper()
	target, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("URL di prova illeggibile %q: %v", raw, err)
	}
	return g.CheckTarget(t.Context(), target)
}

// ------------------------------------------------------ letterali nell'URL

// TestLetteraleInternoRifiutatoSenzaRisolvere è il caso che dà il nome alla
// issue: il database di Postqron sta su 127.0.0.1:5433, sulla stessa macchina.
func TestLetteraleInternoRifiutatoSenzaRisolvere(t *testing.T) {
	casi := []string{
		"http://127.0.0.1:5433/",
		"http://127.0.0.1:8080/",
		"http://[::1]:5433/",
		"http://169.254.169.254/latest/meta-data/iam/security-credentials/",
		"http://10.0.0.5/admin",
		"http://192.168.1.1/",
		"https://[fd00::1]/",
		"http://0.0.0.0:5433/",
	}

	dns := fisso(nil)
	g := netguard.New(netguard.Options{Resolver: dns})

	for _, raw := range casi {
		t.Run(raw, func(t *testing.T) {
			if err := controlla(t, g, raw); !errors.Is(err, netguard.ErrNotAllowed) {
				t.Fatalf("errore = %v, atteso ErrNotAllowed", err)
			}
		})
	}
	if dns.totale() != 0 {
		t.Errorf("il risolutore è stato interrogato %d volte per dei letterali: un IP non si risolve, si guarda", dns.totale())
	}
}

// TestIPv4MappatoInIPv6 è l'aggiramento classico di un blocco scritto due volte,
// una per famiglia di indirizzi.
func TestIPv4MappatoInIPv6(t *testing.T) {
	casi := []string{
		"http://[::ffff:127.0.0.1]:5433/",
		"http://[::ffff:169.254.169.254]/latest/meta-data/",
		"http://[::ffff:10.0.0.1]/",
		"http://[::ffff:7f00:1]:5433/", // lo stesso loopback, in esadecimale
	}

	g := netguard.New(netguard.Options{Resolver: fisso(nil)})
	for _, raw := range casi {
		t.Run(raw, func(t *testing.T) {
			if err := controlla(t, g, raw); !errors.Is(err, netguard.ErrNotAllowed) {
				t.Fatalf("errore = %v, atteso ErrNotAllowed", err)
			}
		})
	}
}

// TestUserinfoNonMaschera: `http://api.example.com@127.0.0.1/` è un URL verso
// example.com per l'occhio di chi legge e un URL verso 127.0.0.1 per qualunque
// client HTTP. Se il guard leggesse la stringa invece dell'host, passerebbe.
func TestUserinfoNonMaschera(t *testing.T) {
	dns := fisso(map[string][]string{"api.example.com": {"93.184.215.14"}})
	g := netguard.New(netguard.Options{Resolver: dns})

	if err := controlla(t, g, "http://api.example.com@127.0.0.1:5433/"); !errors.Is(err, netguard.ErrNotAllowed) {
		t.Fatalf("errore = %v, atteso ErrNotAllowed", err)
	}
	if n := dns.chiamate("api.example.com"); n != 0 {
		t.Errorf("il risolutore ha risolto la userinfo %d volte: sta guardando la parte sbagliata dell'URL", n)
	}
}

// ----------------------------------------------------------------- nomi

// TestNomeCheRisolveInternamente è il vettore che rende inutile qualunque filtro
// sui nomi: un dominio registrato da chiunque, con un record A verso 127.0.0.1.
func TestNomeCheRisolveInternamente(t *testing.T) {
	casi := map[string][]string{
		"loopback.attacco.test": {"127.0.0.1"},
		"metadata.attacco.test": {"169.254.169.254"},
		"privata.attacco.test":  {"10.1.2.3"},
		"mappato.attacco.test":  {"::ffff:127.0.0.1"},
		"v6.attacco.test":       {"::1"},
	}

	g := netguard.New(netguard.Options{Resolver: fisso(casi)})
	for host := range casi {
		t.Run(host, func(t *testing.T) {
			if err := controlla(t, g, "https://"+host+"/health"); !errors.Is(err, netguard.ErrNotAllowed) {
				t.Fatalf("errore = %v, atteso ErrNotAllowed", err)
			}
		})
	}
}

// TestFormeNumericheNonCanoniche copre `http://2130706433/` e `http://0177.0.0.1/`.
//
// Sono 127.0.0.1 scritto in decimale e in ottale. net/netip non li riconosce
// come indirizzi — quindi finiscono nel ramo dei nomi — ma `getaddrinfo(3)` sì,
// e risponde 127.0.0.1. È l'aggiramento di chi confronta la stringa dell'host
// con un elenco di forme note: qui non si confronta la stringa, si guarda ciò
// che il risolutore risponde, e la forma con cui è stata scritta la domanda non
// conta più.
func TestFormeNumericheNonCanoniche(t *testing.T) {
	dns := fisso(map[string][]string{
		"2130706433": {"127.0.0.1"},
		"0177.0.0.1": {"127.0.0.1"},
		"127.1":      {"127.0.0.1"},
	})
	g := netguard.New(netguard.Options{Resolver: dns})

	for _, host := range []string{"2130706433", "0177.0.0.1", "127.1"} {
		t.Run(host, func(t *testing.T) {
			if err := controlla(t, g, "http://"+host+":5433/"); !errors.Is(err, netguard.ErrNotAllowed) {
				t.Fatalf("errore = %v, atteso ErrNotAllowed", err)
			}
		})
	}
}

// TestNomeConIndirizziMisti: un nome che risolve a un indirizzo pubblico *e* a
// uno interno è rifiutato per intero. Tenere «solo la parte buona» significa
// aver accettato che il bersaglio è raggiungibile e aver scelto di non andarci
// adesso — e l'ordine dei record lo decide chi possiede la zona, non noi.
func TestNomeConIndirizziMisti(t *testing.T) {
	casi := map[string][]string{
		"prima-pubblico.test": {"93.184.215.14", "127.0.0.1"},
		"prima-interno.test":  {"127.0.0.1", "93.184.215.14"},
	}

	g := netguard.New(netguard.Options{Resolver: fisso(casi)})
	for host := range casi {
		t.Run(host, func(t *testing.T) {
			if err := controlla(t, g, "https://"+host+"/"); !errors.Is(err, netguard.ErrNotAllowed) {
				t.Fatalf("errore = %v, atteso ErrNotAllowed", err)
			}
		})
	}
}

func TestNomePubblicoAmmesso(t *testing.T) {
	dns := fisso(map[string][]string{
		"api.example.com": {"93.184.215.14"},
		"solo-v6.test":    {"2606:4700::1111"},
	})
	g := netguard.New(netguard.Options{Resolver: dns})

	for _, raw := range []string{"https://api.example.com/tasks/digest", "https://solo-v6.test/"} {
		if err := controlla(t, g, raw); err != nil {
			t.Fatalf("%s è stato rifiutato: %v", raw, err)
		}
	}
}

// TestNomeNonRisolvibileAmmesso fissa una scelta, non un caso limite.
//
// Un nome che oggi non risolve non è un nome interno: è un nome che non
// risolve. Rifiutarlo farebbe fallire la modifica di un job per un guasto del
// DNS che non riguarda l'utente, non guadagnerebbe niente — quel nome viene
// ricontrollato alla connessione, dove il rifiuto conta — e renderebbe
// «accettato» sinonimo esatto di «pubblico», che è un oracolo più nitido di
// quello che si vuole lasciare.
func TestNomeNonRisolvibileAmmesso(t *testing.T) {
	dns := fisso(map[string][]string{})
	g := netguard.New(netguard.Options{Resolver: dns})

	if err := controlla(t, g, "https://non-esiste.test/"); err != nil {
		t.Fatalf("un nome non risolvibile è stato rifiutato alla validazione: %v", err)
	}
	if dns.chiamate("non-esiste.test") == 0 {
		t.Error("il nome non è stato nemmeno risolto")
	}
}

// TestSchemaNonAmmesso: il guard non si fida del chiamante nemmeno per lo
// schema, che pure jobs.validateURL controlla già.
func TestSchemaNonAmmesso(t *testing.T) {
	g := netguard.New(netguard.Options{Resolver: fisso(nil)})
	for _, raw := range []string{"file:///etc/passwd", "gopher://example.com/", "ftp://example.com/"} {
		if err := controlla(t, g, raw); !errors.Is(err, netguard.ErrNotAllowed) {
			t.Errorf("%s: errore = %v, atteso ErrNotAllowed", raw, err)
		}
	}
}

// --------------------------------------------------- il messaggio d'errore

// TestIlMessaggioNonDiceCosaHaTrovato è il test dell'oracolo.
//
// Se il rifiuto di `loopback.test` dicesse qualcosa di diverso dal rifiuto di
// `metadata.test`, la nostra API diventerebbe uno strumento per mappare la
// nostra rete: si sottopongono nomi e si legge la risposta. Il messaggio deve
// essere lo stesso identico testo in tutti i casi, e non deve contenere né il
// nome né l'indirizzo.
func TestIlMessaggioNonDiceCosaHaTrovato(t *testing.T) {
	dns := fisso(map[string][]string{
		"loopback.test": {"127.0.0.1"},
		"metadata.test": {"169.254.169.254"},
		"privata.test":  {"10.9.9.9"},
		"mappato.test":  {"::ffff:127.0.0.1"},
		"nostra.test":   {"203.0.114.7"}, // rifiutato dal Deny del deployment
	})
	g := netguard.New(netguard.Options{
		Policy:   netguard.Policy{Deny: []netip.Prefix{netip.MustParsePrefix("203.0.114.7/32")}},
		Resolver: dns,
	})

	host := []string{"loopback.test", "metadata.test", "privata.test", "mappato.test", "nostra.test"}
	messaggi := map[string]bool{}
	for _, h := range host {
		err := controlla(t, g, "https://"+h+"/")
		if err == nil {
			t.Fatalf("%s non è stato rifiutato", h)
		}
		messaggi[err.Error()] = true

		// Né il nome sottoposto né l'indirizzo trovato devono comparire: sono
		// entrambi la risposta alla domanda che l'attaccante sta ponendo.
		for _, segreto := range []string{h, "127.0.0.1", "169.254", "10.9.9.9", "203.0.114", "loopback", "link-local", "privata", "interno"} {
			if strings.Contains(strings.ToLower(err.Error()), strings.ToLower(segreto)) {
				t.Errorf("il messaggio per %s contiene %q: %s", h, segreto, err.Error())
			}
		}
	}
	if len(messaggi) != 1 {
		t.Fatalf("%d messaggi diversi per %d rifiuti: la differenza è l'oracolo (%v)", len(messaggi), len(host), messaggi)
	}
}

// TestLaRagioneFinisceNelLog è l'altra metà della scelta precedente: il
// dettaglio non sparisce, cambia destinatario. Senza, un rifiuto contestato non
// sarebbe diagnosticabile da nessuno.
func TestLaRagioneFinisceNelLog(t *testing.T) {
	var p netguard.Policy
	ok, reason := p.Allows(netip.MustParseAddr("169.254.169.254"))
	if ok {
		t.Fatal("169.254.169.254 ammesso")
	}
	if !strings.Contains(reason, "169.254.169.254") {
		t.Errorf("la ragione per l'operatore non nomina il metadata cloud: %q", reason)
	}
}
