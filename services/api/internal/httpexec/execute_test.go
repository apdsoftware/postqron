package httpexec_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/apdsoftware/postqron/services/api/internal/httpexec"
)

// TestLaRichiestaPortaMetodoHeaderECorpo verifica la parte di R1 che l'esecutore
// deve tradurre in una richiesta vera.
func TestLaRichiestaPortaMetodoHeaderECorpo(t *testing.T) {
	t.Parallel()

	reg := &registro{}
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reg.aggiungi(leggi(t, r))
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, "ricevuto")
	}))
	defer target.Close()

	exec := nuovoEsecutore(t, target.Client())
	res, err := exec.Execute(t.Context(), occorrenza(t, jobSpec{
		URL:     target.URL + "/hook?a=1",
		Method:  "post",
		Headers: map[string]string{"X-Postqron-Token": "abc", "Content-Type": "application/json"},
		Body:    `{"ping":true}`,
	}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if res.ResponseStatus != http.StatusCreated {
		t.Errorf("status = %d, atteso %d", res.ResponseStatus, http.StatusCreated)
	}
	if res.ResponseExcerpt != "ricevuto" {
		t.Errorf("estratto = %q, atteso %q", res.ResponseExcerpt, "ricevuto")
	}

	viste := reg.tutte()
	if len(viste) != 1 {
		t.Fatalf("richieste ricevute = %d, attesa 1", len(viste))
	}
	got := viste[0]
	// Il metodo arriva dal database in maiuscolo (tipo `http_method`), ma un
	// metodo scritto in minuscolo non deve diventare un metodo diverso.
	if got.Method != http.MethodPost {
		t.Errorf("metodo = %q, atteso POST", got.Method)
	}
	if got.Path != "/hook" || got.Query != "a=1" {
		t.Errorf("percorso = %q?%q, atteso /hook?a=1", got.Path, got.Query)
	}
	if got.Body != `{"ping":true}` {
		t.Errorf("corpo = %q", got.Body)
	}
	if v := got.Headers.Get("X-Postqron-Token"); v != "abc" {
		t.Errorf("header del job non arrivato: X-Postqron-Token = %q", v)
	}
	if v := got.Headers.Get("User-Agent"); v != httpexec.DefaultUserAgent {
		t.Errorf("User-Agent = %q, atteso %q", v, httpexec.DefaultUserAgent)
	}
}

// TestGliHeaderDelJobHannoLaPrecedenza: lo `User-Agent` predefinito serve a
// farci riconoscere (R39), non a impedire all'utente di presentarsi come vuole.
// `Host` è il caso speciale, perché in Go non vive nella mappa delle testate.
func TestGliHeaderDelJobHannoLaPrecedenza(t *testing.T) {
	t.Parallel()

	reg := &registro{}
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reg.aggiungi(leggi(t, r))
	}))
	defer target.Close()

	exec := nuovoEsecutore(t, target.Client())
	if _, err := exec.Execute(t.Context(), occorrenza(t, jobSpec{
		URL:     target.URL,
		Headers: map[string]string{"User-Agent": "cliente/2.0", "Host": "api.interno.example"},
	})); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	viste := reg.tutte()
	if len(viste) != 1 {
		t.Fatalf("richieste ricevute = %d, attesa 1", len(viste))
	}
	if v := viste[0].Headers.Get("User-Agent"); v != "cliente/2.0" {
		t.Errorf("User-Agent = %q, atteso quello del job", v)
	}
	if viste[0].Host != "api.interno.example" {
		t.Errorf("Host = %q, atteso quello del job", viste[0].Host)
	}
}

// TestUnaRispostaEnormeNonArrivaInMemoria è R40 sul lato della risposta: il
// tetto è del servizio e non del job, e vale sulla lettura, non solo su ciò che
// si conserva.
func TestUnaRispostaEnormeNonArrivaInMemoria(t *testing.T) {
	t.Parallel()

	const tetto = 1024
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		blocco := strings.Repeat("x", 64*1024)
		for range 16 { // un megabyte: mille volte il tetto di questa prova
			if _, err := w.Write([]byte(blocco)); err != nil {
				return
			}
		}
	}))
	defer target.Close()

	exec := nuovoEsecutore(t, target.Client(), func(o *httpexec.Options) { o.MaxResponseBytes = tetto })
	res, err := exec.Execute(t.Context(), occorrenza(t, jobSpec{URL: target.URL}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(res.ResponseExcerpt) != tetto {
		t.Errorf("estratto di %d byte, atteso il tetto di %d", len(res.ResponseExcerpt), tetto)
	}
}

// TestUnCorpoBinarioRestaScrivibile: `response_excerpt` è una colonna `text`, e
// una risposta è una sequenza di byte qualunque. Un byte non UTF-8 o un NUL
// farebbero fallire la scrittura dell'esito — cioè perdere il fatto che
// l'esecuzione è avvenuta, non solo il suo estratto.
func TestUnCorpoBinarioRestaScrivibile(t *testing.T) {
	t.Parallel()

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Byte non validi in UTF-8, un NUL, e in coda il primo byte di un
		// carattere multibyte senza il suo seguito — la forma che produce un
		// troncamento a metà carattere.
		if _, err := w.Write([]byte{0xff, 0xfe, 'o', 'k', 0x00, 0xc3}); err != nil {
			return
		}
	}))
	defer target.Close()

	exec := nuovoEsecutore(t, target.Client())
	res, err := exec.Execute(t.Context(), occorrenza(t, jobSpec{URL: target.URL}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !utf8.ValidString(res.ResponseExcerpt) {
		t.Errorf("estratto non UTF-8 valido: %q", res.ResponseExcerpt)
	}
	if strings.ContainsRune(res.ResponseExcerpt, 0) {
		t.Errorf("estratto con NUL: PostgreSQL rifiuterebbe la scrittura dell'esito: %q", res.ResponseExcerpt)
	}
	if !strings.Contains(res.ResponseExcerpt, "ok") {
		t.Errorf("la parte leggibile è andata persa: %q", res.ResponseExcerpt)
	}
}

// TestIlTimeoutCopreLInteraRichiesta è il caso classico che un timeout sulla
// sola connessione lascia passare: il bersaglio accetta e poi non risponde mai.
func TestIlTimeoutCopreLInteraRichiesta(t *testing.T) {
	t.Parallel()

	target := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer target.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 150*time.Millisecond)
	defer cancel()

	exec := nuovoEsecutore(t, target.Client())
	inizio := time.Now()
	_, err := exec.Execute(ctx, occorrenza(t, jobSpec{URL: target.URL}))
	durata := time.Since(inizio)

	if err == nil {
		t.Fatal("Execute non ha restituito errore: il worker sarebbe rimasto occupato")
	}
	// È la condizione su cui internal/dispatch distingue `timed_out` da `failed`.
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("errore = %v, atteso context.DeadlineExceeded", err)
	}
	if durata > 5*time.Second {
		t.Errorf("la richiesta è durata %s: il contesto non è stato rispettato", durata)
	}
}

// TestIlTimeoutCopreAncheLaLetturaDelCorpo: le testate arrivano subito, il corpo
// mai. Senza il contesto sulla richiesta questa è un'attesa senza fine.
func TestIlTimeoutCopreAncheLaLetturaDelCorpo(t *testing.T) {
	t.Parallel()

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-r.Context().Done()
	}))
	defer target.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 150*time.Millisecond)
	defer cancel()

	exec := nuovoEsecutore(t, target.Client())
	res, err := exec.Execute(ctx, occorrenza(t, jobSpec{URL: target.URL}))
	if err == nil {
		t.Fatal("Execute non ha restituito errore")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("errore = %v, atteso context.DeadlineExceeded", err)
	}
	// Lo status è arrivato: è un fatto, e va registrato anche se la risposta non
	// si è conclusa.
	if res.ResponseStatus != http.StatusOK {
		t.Errorf("status = %d, atteso 200 conservato nonostante l'errore", res.ResponseStatus)
	}
}

// TestUnoStatusDiErroreNonEUnErroreDellEsecutore è la terza clausola del
// contratto di dispatch.Executor: la richiesta è arrivata a destinazione, e
// decidere che esito scriverne è di chi possiede la riga.
func TestUnoStatusDiErroreNonEUnErroreDellEsecutore(t *testing.T) {
	t.Parallel()

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, "boom")
	}))
	defer target.Close()

	exec := nuovoEsecutore(t, target.Client())
	res, err := exec.Execute(t.Context(), occorrenza(t, jobSpec{URL: target.URL}))
	if err != nil {
		t.Fatalf("un 500 non è un errore dell'esecutore, ma ne è tornato uno: %v", err)
	}
	if res.ResponseStatus != http.StatusInternalServerError {
		t.Errorf("status = %d, atteso 500", res.ResponseStatus)
	}
	if res.ResponseExcerpt != "boom" {
		t.Errorf("estratto = %q, atteso %q", res.ResponseExcerpt, "boom")
	}
}

// TestLURLNonCompareNellErrore è R43 sul percorso più facile da dimenticare.
//
// `http.Client` avvolge ogni errore in un `*url.Error`, che stampa l'URL
// completo. Dal momento in cui i segreti del workspace vengono risolti in URL e
// header (#458), quel testo scritto nella colonna `error` è un segreto
// consegnato all'API che lo rilegge.
func TestLURLNonCompareNellErrore(t *testing.T) {
	t.Parallel()

	const segreto = "tok_segretissimo_del_workspace"
	indirizzo := portaChiusa(t)

	exec := nuovoEsecutore(t, &http.Client{})
	_, err := exec.Execute(t.Context(), occorrenza(t, jobSpec{
		URL: "http://" + indirizzo + "/hook?token=" + segreto,
	}))
	if err == nil {
		t.Fatal("Execute non ha restituito errore su una porta chiusa")
	}
	if strings.Contains(err.Error(), segreto) {
		t.Errorf("il segreto è finito nell'errore, che l'utente rilegge dall'API: %v", err)
	}
	// La causa resta: senza, l'errore non spiegherebbe più niente.
	if !strings.Contains(err.Error(), "refused") && !strings.Contains(err.Error(), "connect") {
		t.Errorf("la causa del guasto è andata persa: %v", err)
	}
}

// TestLaVerificaTLSRestaAttiva: il certificato di httptest.NewTLSServer è
// autofirmato, e un client con la verifica disattivata lo accetterebbe senza
// dire niente. È il modo di accorgersi che qualcuno l'ha disattivata qui dentro.
func TestLaVerificaTLSRestaAttiva(t *testing.T) {
	t.Parallel()

	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "non dovresti vedermi")
	}))
	defer target.Close()

	// Un client qualunque, con il trasporto predefinito della libreria standard:
	// l'esecutore non deve né disattivare la verifica né aggiungere certificati.
	exec := nuovoEsecutore(t, &http.Client{})
	_, err := exec.Execute(t.Context(), occorrenza(t, jobSpec{URL: target.URL}))
	if err == nil {
		t.Fatal("un certificato autofirmato è stato accettato: la verifica TLS è disattivata")
	}
	if !strings.Contains(err.Error(), "certificate") && !strings.Contains(err.Error(), "x509") {
		t.Errorf("errore = %v, atteso un rifiuto del certificato", err)
	}
}

// TestLaPoliticaSuiRedirectEQuellaDelClient: il tetto ai salti (R40) e il
// controllo su ogni connessione (R38) vivono nel client di netguard. La prova
// che conta qui è che l'esecutore lo usi come gli è stato dato, invece di
// sostituirne la politica con la propria.
func TestLaPoliticaSuiRedirectEQuellaDelClient(t *testing.T) {
	t.Parallel()

	var salti int
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/fine" {
			fmt.Fprint(w, "arrivato")
			return
		}
		salti++
		http.Redirect(w, r, "/fine", http.StatusFound)
	}))
	defer target.Close()

	rifiutato := errors.New("troppi redirect")
	client := target.Client()
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return rifiutato }

	exec := nuovoEsecutore(t, client)
	_, err := exec.Execute(t.Context(), occorrenza(t, jobSpec{URL: target.URL + "/parti"}))
	if err == nil {
		t.Fatal("il redirect è stato seguito nonostante la politica del client lo vietasse")
	}
	if !errors.Is(err, rifiutato) {
		t.Errorf("errore = %v, atteso quello della politica del client", err)
	}
	if salti != 1 {
		t.Errorf("salti serviti = %d, atteso 1", salti)
	}
}

// TestIlRedirectVieneSeguitoQuandoIlClientLoConsente è la controprova della
// precedente: senza politica contraria, la catena si percorre e il corpo viene
// ripetuto sui salti che lo prevedono.
func TestIlRedirectVieneSeguitoQuandoIlClientLoConsente(t *testing.T) {
	t.Parallel()

	reg := &registro{}
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reg.aggiungi(leggi(t, r))
		if r.URL.Path == "/fine" {
			fmt.Fprint(w, "arrivato")
			return
		}
		http.Redirect(w, r, "/fine", http.StatusTemporaryRedirect)
	}))
	defer target.Close()

	exec := nuovoEsecutore(t, target.Client())
	res, err := exec.Execute(t.Context(), occorrenza(t, jobSpec{
		URL:    target.URL + "/parti",
		Method: http.MethodPost,
		Body:   "carico",
	}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.ResponseExcerpt != "arrivato" {
		t.Errorf("estratto = %q, atteso %q", res.ResponseExcerpt, "arrivato")
	}

	viste := reg.tutte()
	if len(viste) != 2 {
		t.Fatalf("richieste servite = %d, attese 2 (originale + redirect)", len(viste))
	}
	// Il 307 conserva metodo e corpo: senza GetBody sulla richiesta, il secondo
	// salto arriverebbe vuoto.
	if viste[1].Method != http.MethodPost || viste[1].Body != "carico" {
		t.Errorf("il redirect ha perso metodo o corpo: %s %q", viste[1].Method, viste[1].Body)
	}
}

// TestUnURLIllegibileNonProduceUnaRichiesta: l'errore deve dire cosa non va
// senza ripetere il valore, che può contenere un segreto risolto.
func TestUnURLIllegibileNonProduceUnaRichiesta(t *testing.T) {
	t.Parallel()

	casi := map[string]string{
		"schema non ammesso": "file:///etc/passwd?token=tok_segreto",
		"URL malformato":     "http://esempio.example/%zz?token=tok_segreto",
	}
	for nome, indirizzo := range casi {
		t.Run(nome, func(t *testing.T) {
			t.Parallel()
			exec := nuovoEsecutore(t, &http.Client{})
			_, err := exec.Execute(t.Context(), occorrenza(t, jobSpec{URL: indirizzo}))
			if err == nil {
				t.Fatalf("URL %q accettato", indirizzo)
			}
			if strings.Contains(err.Error(), "tok_segreto") {
				t.Errorf("il valore è finito nell'errore: %v", err)
			}
		})
	}
}

// TestSenzaGuardNonSiCostruisce: non esiste un default plausibile, perché
// l'unico default plausibile sarebbe un client non protetto (R38).
func TestSenzaGuardNonSiCostruisce(t *testing.T) {
	t.Parallel()

	if _, err := httpexec.New(httpexec.Options{}); err == nil {
		t.Fatal("New ha accettato un esecutore senza guard")
	}
}

// TestIlClientSiChiedeUnaVoltaSola: il client porta con sé il proprio pool di
// connessioni. Chiederne uno nuovo a ogni richiesta significherebbe una
// connessione nuova a ogni richiesta, e su un job a un secondo si vedrebbe.
func TestIlClientSiChiedeUnaVoltaSola(t *testing.T) {
	t.Parallel()

	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer target.Close()

	guard := &guardDiProva{client: target.Client()}
	exec, err := httpexec.New(httpexec.Options{Guard: guard})
	if err != nil {
		t.Fatalf("httpexec.New: %v", err)
	}
	for range 3 {
		if _, err := exec.Execute(t.Context(), occorrenza(t, jobSpec{URL: target.URL})); err != nil {
			t.Fatalf("Execute: %v", err)
		}
	}
	if n := guard.chiamate.Load(); n != 1 {
		t.Errorf("Client() chiamato %d volte, attesa 1", n)
	}
}
