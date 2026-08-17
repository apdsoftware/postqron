package netguard_test

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/apdsoftware/postqron/services/api/internal/netguard"
)

// policyDiProva deroga al blocco per il solo loopback.
//
// I server di prova vivono su 127.0.0.1, cioè esattamente su ciò che il
// pacchetto ha il compito di rifiutare: senza la deroga non esisterebbe un
// bersaglio «ammesso» con cui provare il primo salto di un redirect, e il test
// sul secondo salto non proverebbe niente. La deroga non è raggiungibile
// dall'API pubblica — vedi export_test.go.
func policyDiProva() netguard.Policy {
	return netguard.AllowForTest(netguard.Policy{},
		netip.MustParsePrefix("127.0.0.0/8"),
		netip.MustParsePrefix("::1/128"))
}

// portaDi estrae la porta di un server di prova.
func portaDi(t *testing.T, raw string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("URL del server di prova illeggibile: %v", err)
	}
	return u.Port()
}

// TestConnessioneVersoBersaglioAmmesso è la riga di base: senza questa, ogni
// test che segue passerebbe anche con un guard che rifiuta tutto.
func TestConnessioneVersoBersaglioAmmesso(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	defer srv.Close()

	dns := fisso(map[string][]string{"pubblico.test": {"127.0.0.1"}})
	g := netguard.New(netguard.Options{Policy: policyDiProva(), Resolver: dns})

	resp, err := g.Client().Get("http://pubblico.test:" + portaDi(t, srv.URL) + "/")
	if err != nil {
		t.Fatalf("una destinazione ammessa è stata rifiutata: %v", err)
	}
	defer resp.Body.Close()

	corpo, _ := io.ReadAll(resp.Body)
	if string(corpo) != "ok" {
		t.Fatalf("corpo = %q", corpo)
	}
}

// TestRedirectVersoIndirizzoInterno è il secondo vettore della issue.
//
// Un bersaglio perfettamente pubblico risponde `302 Location:
// http://169.254.169.254/…`. Un controllo fatto solo alla validazione, o solo
// sul primo URL, ha già detto sì e non viene più consultato. Il test verifica
// **entrambe** le cose che contano: che il primo salto sia stato davvero
// servito (altrimenti il blocco sarebbe scattato prima, e non staremmo provando
// i redirect) e che il secondo non sia partito.
func TestRedirectVersoIndirizzoInterno(t *testing.T) {
	var primoSalto atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primoSalto.Add(1)
		http.Redirect(w, r, "http://metadata.test/latest/meta-data/iam/security-credentials/", http.StatusFound)
	}))
	defer srv.Close()

	dns := fisso(map[string][]string{
		"pubblico.test": {"127.0.0.1"},
		"metadata.test": {"169.254.169.254"},
	})
	g := netguard.New(netguard.Options{Policy: policyDiProva(), Resolver: dns})

	target := "http://pubblico.test:" + portaDi(t, srv.URL) + "/"

	// La validazione dice sì, ed è corretto che lo dica: l'URL che l'utente ha
	// scritto è pubblico. È tutto ciò che un controllo alla validazione può
	// sapere.
	if err := controlla(t, g, target); err != nil {
		t.Fatalf("il primo URL, che è pubblico, è stato rifiutato: %v", err)
	}

	_, err := g.Client().Get(target)
	if !errors.Is(err, netguard.ErrNotAllowed) {
		t.Fatalf("errore = %v, atteso ErrNotAllowed: il redirect ha raggiunto il metadata cloud", err)
	}
	if primoSalto.Load() != 1 {
		t.Fatalf("il primo salto è stato servito %d volte: il blocco è scattato altrove e il test non prova i redirect", primoSalto.Load())
	}
}

// TestRedirectVersoLoopback è la stessa cosa puntata al nostro database.
func TestRedirectVersoLoopback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://interno.test:5433/", http.StatusMovedPermanently)
	}))
	defer srv.Close()

	// Il loopback è ammesso solo per l'host del server di prova: `interno.test`
	// risolve a un indirizzo privato, che nessuna deroga copre.
	dns := fisso(map[string][]string{
		"pubblico.test": {"127.0.0.1"},
		"interno.test":  {"10.0.0.5"},
	})
	g := netguard.New(netguard.Options{Policy: policyDiProva(), Resolver: dns})

	_, err := g.Client().Get("http://pubblico.test:" + portaDi(t, srv.URL) + "/")
	if !errors.Is(err, netguard.ErrNotAllowed) {
		t.Fatalf("errore = %v, atteso ErrNotAllowed", err)
	}
}

// TestRedirectVersoSchemaNonAmmesso: `Location: file:///etc/passwd`.
func TestRedirectVersoSchemaNonAmmesso(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "file:///etc/passwd", http.StatusFound)
	}))
	defer srv.Close()

	dns := fisso(map[string][]string{"pubblico.test": {"127.0.0.1"}})
	g := netguard.New(netguard.Options{Policy: policyDiProva(), Resolver: dns})

	_, err := g.Client().Get("http://pubblico.test:" + portaDi(t, srv.URL) + "/")
	if !errors.Is(err, netguard.ErrNotAllowed) {
		t.Fatalf("errore = %v, atteso ErrNotAllowed", err)
	}
}

// TestDNSRebinding è il terzo vettore, ed è quello su cui quasi tutte le
// implementazioni cadono.
//
// Il nome risolve a un indirizzo **pubblico** quando lo si valida, e a
// 127.0.0.1 quando ci si connette. Un'implementazione che valida l'indirizzo e
// poi passa *l'URL* a un client HTTP fa esattamente questo: due risoluzioni, e
// la seconda è quella che conta. Qui la seconda risoluzione è la sola che apre
// una connessione, e il controllo è su di lei.
func TestDNSRebinding(t *testing.T) {
	dns := nuovoRisolutore(func(chiamata int, host string) ([]netip.Addr, error) {
		if host != "rebind.test" {
			return nil, &erroreDNS{host: host}
		}
		if chiamata == 1 {
			return indirizzi("93.184.215.14"), nil // alla validazione: pubblico
		}
		return indirizzi("127.0.0.1"), nil // alla connessione: il nostro database
	})
	// Nessuna deroga: qui il loopback dev'essere rifiutato per davvero.
	g := netguard.New(netguard.Options{Resolver: dns})

	if err := controlla(t, g, "http://rebind.test:5433/"); err != nil {
		t.Fatalf("la validazione ha rifiutato un indirizzo pubblico: %v", err)
	}

	_, err := g.Client().Get("http://rebind.test:5433/")
	if !errors.Is(err, netguard.ErrNotAllowed) {
		t.Fatalf("errore = %v, atteso ErrNotAllowed: il rebinding ha superato il blocco", err)
	}
}

// TestUnaSolaRisoluzionePerConnessione prova la proprietà da cui dipende tutto
// il resto: fra il controllo dell'indirizzo e la `connect` non c'è una seconda
// risoluzione.
//
// Il risolutore risponde 127.0.0.1 alla prima domanda e 169.254.169.254 alla
// seconda. Se il dialer, dopo aver validato, passasse il *nome* al sistema — che
// è la forma sbagliata e diffusa — la richiesta finirebbe sul metadata cloud
// oppure verrebbe rifiutata: in nessuno dei due casi arriverebbe la risposta del
// server. Che arrivi, e che la domanda sia stata posta una volta sola, è la
// prova che ci si connette all'indirizzo già validato.
func TestUnaSolaRisoluzionePerConnessione(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "pinned")
	}))
	defer srv.Close()

	dns := nuovoRisolutore(func(chiamata int, host string) ([]netip.Addr, error) {
		if host != "pin.test" {
			return nil, &erroreDNS{host: host}
		}
		if chiamata == 1 {
			return indirizzi("127.0.0.1"), nil
		}
		return indirizzi("169.254.169.254"), nil
	})
	g := netguard.New(netguard.Options{Policy: policyDiProva(), Resolver: dns})

	resp, err := g.Client().Get("http://pin.test:" + portaDi(t, srv.URL) + "/")
	if err != nil {
		t.Fatalf("la richiesta è fallita: %v", err)
	}
	defer resp.Body.Close()

	corpo, _ := io.ReadAll(resp.Body)
	if string(corpo) != "pinned" {
		t.Fatalf("corpo = %q: la connessione non è finita sul server validato", corpo)
	}
	if n := dns.chiamate("pin.test"); n != 1 {
		t.Fatalf("il nome è stato risolto %d volte per una connessione: fra il controllo e la connect c'è una finestra", n)
	}
}

// TestLetteraleNonRisolveMai: un URL con un indirizzo dentro non deve produrre
// una domanda DNS, in nessuno dei due punti di controllo.
func TestLetteraleNonRisolveMai(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	defer srv.Close()

	dns := fisso(nil)
	g := netguard.New(netguard.Options{Policy: policyDiProva(), Resolver: dns})

	resp, err := g.Client().Get(srv.URL)
	if err != nil {
		t.Fatalf("richiesta fallita: %v", err)
	}
	_ = resp.Body.Close()

	if dns.totale() != 0 {
		t.Fatalf("il risolutore è stato interrogato %d volte per un letterale", dns.totale())
	}
}

// TestTroppiRedirect applica il tetto di R40 alla catena.
func TestTroppiRedirect(t *testing.T) {
	var salti atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		salti.Add(1)
		http.Redirect(w, r, "/ancora", http.StatusFound)
	}))
	defer srv.Close()

	dns := fisso(map[string][]string{"pubblico.test": {"127.0.0.1"}})
	g := netguard.New(netguard.Options{Policy: policyDiProva(), Resolver: dns, MaxRedirects: 3})

	_, err := g.Client().Get("http://pubblico.test:" + portaDi(t, srv.URL) + "/")
	if !errors.Is(err, netguard.ErrTooManyRedirects) {
		t.Fatalf("errore = %v, atteso ErrTooManyRedirects", err)
	}
	if salti.Load() > 4 {
		t.Fatalf("%d salti serviti con un tetto di 3", salti.Load())
	}
}

// TestProxyDellAmbienteIgnorato chiude un aggiramento che non richiede
// all'attaccante nulla: basta che l'ambiente del processo abbia `HTTP_PROXY`.
//
// Con il trasporto predefinito di Go la connessione andrebbe **al proxy** — un
// indirizzo che il nostro dialer approverebbe senza esitare — e sarebbe il proxy
// a raggiungere la destinazione interna per conto nostro. Il blocco sparirebbe
// per una variabile d'ambiente.
func TestProxyDellAmbienteIgnorato(t *testing.T) {
	var servito atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		servito.Add(1)
		_, _ = io.WriteString(w, "diretto")
	}))
	defer srv.Close()

	t.Setenv("HTTP_PROXY", "http://10.0.0.9:3128")
	t.Setenv("HTTPS_PROXY", "http://10.0.0.9:3128")
	t.Setenv("ALL_PROXY", "http://10.0.0.9:3128")

	dns := fisso(map[string][]string{"pubblico.test": {"127.0.0.1"}})
	g := netguard.New(netguard.Options{Policy: policyDiProva(), Resolver: dns})

	resp, err := g.Client().Get("http://pubblico.test:" + portaDi(t, srv.URL) + "/")
	if err != nil {
		t.Fatalf("richiesta fallita: %v", err)
	}
	defer resp.Body.Close()

	corpo, _ := io.ReadAll(resp.Body)
	if string(corpo) != "diretto" || servito.Load() != 1 {
		t.Fatalf("la richiesta non è andata diretta al bersaglio: corpo = %q, servite = %d", corpo, servito.Load())
	}
}

// TestControlloPrimaDellaConnect esercita l'ultima rete di sicurezza.
//
// In esercizio non scatta mai — il codice sopra ha già rifiutato tutto ciò che
// rifiuterebbe — ed è precisamente per questo che va chiamata a mano: un
// controllo che non si vede mai fallire è un controllo di cui nessuno sa se
// funziona.
func TestControlloPrimaDellaConnect(t *testing.T) {
	g := netguard.New(netguard.Options{})

	rifiutati := []string{
		"127.0.0.1:5433",
		"[::1]:5433",
		"169.254.169.254:80",
		"10.0.0.1:80",
		"[::ffff:127.0.0.1]:5433",
		"non-un-indirizzo", // illeggibile: se non sappiamo dove andiamo, non ci andiamo
		"pubblico.test:80", // un nome qui è già un errore: doveva essere un letterale
	}
	for _, address := range rifiutati {
		if err := netguard.ControlForTest(g, address); !errors.Is(err, netguard.ErrNotAllowed) {
			t.Errorf("%s: errore = %v, atteso ErrNotAllowed", address, err)
		}
	}

	for _, address := range []string{"93.184.215.14:443", "[2606:4700::1111]:443"} {
		if err := netguard.ControlForTest(g, address); err != nil {
			t.Errorf("%s è stato rifiutato: %v", address, err)
		}
	}
}

// TestDialContextRifiutaDirettamente verifica il blocco senza passare dal client
// HTTP: è il punto di innesto che il worker pool (#389) userà.
func TestDialContextRifiutaDirettamente(t *testing.T) {
	dns := fisso(map[string][]string{"interno.test": {"192.168.1.10"}})
	g := netguard.New(netguard.Options{Resolver: dns})

	if _, err := g.DialContext(t.Context(), "tcp", "interno.test:80"); !errors.Is(err, netguard.ErrNotAllowed) {
		t.Fatalf("errore = %v, atteso ErrNotAllowed", err)
	}
	if _, err := g.DialContext(t.Context(), "tcp", "127.0.0.1:5433"); !errors.Is(err, netguard.ErrNotAllowed) {
		t.Fatalf("errore = %v, atteso ErrNotAllowed", err)
	}
}

// TestErroreDiRisoluzioneInEsecuzioneNonEUnRifiuto distingue i due casi nel
// registro delle esecuzioni: un nome che non esiste più è un guasto della
// destinazione, e va detto com'è. Confonderlo con il rifiuto renderebbe
// incomprensibile il primo e non nasconderebbe niente del secondo — chiunque può
// interrogare il DNS pubblico da casa propria.
func TestErroreDiRisoluzioneInEsecuzioneNonEUnRifiuto(t *testing.T) {
	g := netguard.New(netguard.Options{Resolver: fisso(nil)})

	_, err := g.DialContext(t.Context(), "tcp", "sparito.test:443")
	if err == nil {
		t.Fatal("una destinazione non risolvibile ha aperto una connessione")
	}
	if errors.Is(err, netguard.ErrNotAllowed) {
		t.Fatalf("un guasto del DNS è stato riportato come rifiuto: %v", err)
	}
	if !strings.Contains(err.Error(), "no such host") {
		t.Errorf("l'errore non dice cosa è successo: %v", err)
	}
}
