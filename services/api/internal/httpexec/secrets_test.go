package httpexec_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/apdsoftware/postqron/services/api/internal/httpexec"
)

// Le prove di R43 sul lato dell'esecuzione: i segreti del workspace vengono
// risolti nella richiesta, e non tornano indietro nel registro.
//
// # Perché ogni caso comincia con un controllo che sembra inutile
//
// Un test di non-perdita che passa perché il segreto non c'era è peggio di un
// test assente, perché autorizza a non guardare più. Prima di verificare che il
// valore *non* compaia dove non deve, ogni caso verifica che ci sia dove deve —
// nella richiesta che il bersaglio ha ricevuto, nel corpo che ha rimandato
// indietro, nell'errore che è stato prodotto. È la stessa forma di
// `TestExecutorContract` in internal/secrets, che di questo lavoro è la
// specifica.

// valoreSegreto è la finta credenziale del cliente. Lunga abbastanza da essere
// accettata (secrets.MinValueLength) e riconoscibile a colpo d'occhio in un
// messaggio d'errore.
const valoreSegreto = "finta-credenziale-del-cliente"

// TestIRiferimentiAiSegretiArrivanoRisoltiAlBersaglio è R43 nella sua parte
// visibile: `${DIGEST_TOKEN}` non deve arrivare al bersaglio come testo.
//
// I tre campi sono provati insieme perché la spec li nomina insieme — «iniettati
// in URL, header e corpo» — e perché è esattamente il genere di elenco da cui
// sparisce un elemento senza che nessuno se ne accorga.
func TestIRiferimentiAiSegretiArrivanoRisoltiAlBersaglio(t *testing.T) {
	t.Parallel()

	reg := &registro{}
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reg.aggiungi(leggi(t, r))
		fmt.Fprint(w, "ok")
	}))
	defer target.Close()

	exec := nuovoEsecutore(t, target.Client(), func(o *httpexec.Options) {
		o.Secrets = segretiDiProva(t, map[string]string{"DIGEST_TOKEN": valoreSegreto})
	})
	if _, err := exec.Execute(t.Context(), occorrenza(t, jobSpec{
		URL:     target.URL + "/digest?token=${DIGEST_TOKEN}",
		Method:  http.MethodPost,
		Headers: map[string]string{"Authorization": "Bearer ${DIGEST_TOKEN}"},
		Body:    `{"token":"${DIGEST_TOKEN}"}`,
	})); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	viste := reg.tutte()
	if len(viste) != 1 {
		t.Fatalf("richieste ricevute = %d, attesa 1", len(viste))
	}
	got := viste[0]

	if got.Query != "token="+valoreSegreto {
		t.Errorf("querystring ricevuta = %q, atteso il segreto risolto", got.Query)
	}
	if v := got.Headers.Get("Authorization"); v != "Bearer "+valoreSegreto {
		t.Errorf("testata ricevuta = %q, atteso il segreto risolto", v)
	}
	if got.Body != `{"token":"`+valoreSegreto+`"}` {
		t.Errorf("corpo ricevuto = %q, atteso il segreto risolto", got.Body)
	}

	// La controprova: nessuno dei tre campi deve contenere ancora il riferimento.
	// Senza, un'espansione che scrivesse il valore *accanto* al `${VAR}` invece
	// che al suo posto passerebbe i controlli qui sopra.
	insieme := got.Query + got.Headers.Get("Authorization") + got.Body
	if strings.Contains(insieme, "${") {
		t.Errorf("un riferimento è arrivato al bersaglio come testo: %q", insieme)
	}
}

// TestUnRiferimentoSenzaSegretoNonFaPartireLaRichiesta è l'altra metà di R43:
// «un riferimento non risolvibile fa fallire la validazione al sync, non
// l'esecuzione alle tre di notte».
//
// Che l'errore arrivi comunque *anche* qui non contraddice la spec, la completa:
// la validazione al sync non può coprire ciò che cambia **dopo** il sync, e un
// segreto revocato ieri sera è esattamente quel caso. L'alternativa sarebbe
// partire con `Bearer ${DIGEST_TOKEN}` scritto così com'è, cioè presentarsi al
// bersaglio con una credenziale sbagliata — che è il modo di farsi bloccare
// l'account, non di fallire con grazia.
func TestUnRiferimentoSenzaSegretoNonFaPartireLaRichiesta(t *testing.T) {
	t.Parallel()

	reg := &registro{}
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reg.aggiungi(leggi(t, r))
	}))
	defer target.Close()

	exec := nuovoEsecutore(t, target.Client())
	res, err := exec.Execute(t.Context(), occorrenza(t, jobSpec{
		URL:     target.URL + "/digest",
		Headers: map[string]string{"Authorization": "Bearer ${DIGEST_TOKEN}"},
	}))
	if err == nil {
		t.Fatal("Execute ha eseguito un job con un riferimento non risolvibile")
	}
	if n := len(reg.tutte()); n != 0 {
		t.Errorf("il bersaglio ha ricevuto %d richieste: la richiesta è partita lo stesso", n)
	}

	// Il testo è quello della validazione al sync, parola per parola: è la stessa
	// [secrets.NameSet.Validate], e chi legge il registro deve trovarci il nome
	// che manca invece di un guasto generico.
	if !strings.Contains(res.ErrorText.String(), "DIGEST_TOKEN") {
		t.Errorf("il testo dell'errore non dice quale segreto manca: %q", res.ErrorText)
	}
}

// TestIlSegretoRiflessoDalBersaglioNonEntraNellEstratto è la seconda clausola del
// contratto (`TestExecutorContract`), verificata sul percorso vero.
//
// Il bersaglio rimanda indietro la credenziale che gli abbiamo mandato, dentro
// il proprio messaggio d'errore. Non è un caso di scuola: «token XYZ non valido»
// è ciò che scrivono parecchie API, ed è il modo in cui un segreto tornerebbe a
// casa nostra e finirebbe in `job_executions.response_excerpt`, che l'utente
// rilegge dall'API e che restiamo a conservare (privacy policy §2.2).
func TestIlSegretoRiflessoDalBersaglioNonEntraNellEstratto(t *testing.T) {
	t.Parallel()

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		// La risposta contiene sia la testata sia la querystring che ci sono
		// arrivate: le due vie da cui un segreto risolto può tornare indietro.
		fmt.Fprintf(w, `{"error":"la credenziale %s non è valida","query":%q}`,
			strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "), r.URL.RawQuery)
	}))
	defer target.Close()

	exec := nuovoEsecutore(t, target.Client(), func(o *httpexec.Options) {
		o.Secrets = segretiDiProva(t, map[string]string{"DIGEST_TOKEN": valoreSegreto})
	})
	res, err := exec.Execute(t.Context(), occorrenza(t, jobSpec{
		URL:     target.URL + "/digest?token=${DIGEST_TOKEN}",
		Headers: map[string]string{"Authorization": "Bearer ${DIGEST_TOKEN}"},
	}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.ResponseStatus != http.StatusUnauthorized {
		t.Fatalf("status = %d, atteso 401: il caso non sta esercitando la risposta che dichiara",
			res.ResponseStatus)
	}

	estratto := res.ResponseExcerpt.String()
	if strings.Contains(estratto, valoreSegreto) {
		t.Errorf("l'estratto conserva la credenziale che il bersaglio ha rimandato indietro:\n  %s\n"+
			"Questo testo finisce in job_executions.response_excerpt.", estratto)
	}
	// Al posto del valore ci va il riferimento: chi legge il registro deve capire
	// perché il testo non torna, altrimenti la redazione sembra un guasto.
	if strings.Count(estratto, "${DIGEST_TOKEN}") != 2 {
		t.Errorf("l'estratto non dice che cosa è stato tolto, o lo dice una volta sola: %s", estratto)
	}
	if !strings.Contains(estratto, "non è valida") {
		t.Errorf("la risposta del bersaglio è andata persa insieme al segreto: %s", estratto)
	}
}

// TestIlSegretoNonEntraNelTestoDellErrore è la terza clausola del contratto, ed
// è la via di fuga meno evidente.
//
// [httpexec.requestError] toglie l'involucro `*url.Error`, che stampa l'URL
// completo, e resta la prima difesa. Questa è la seconda, e serve perché
// togliere l'involucro protegge dal caso noto: **la causa che resta è l'errore
// di qualcun altro** — un driver, una libreria TLS, un redirect — e nessuno di
// loro ha promesso di non citare l'indirizzo a cui stava andando.
//
// Il trasporto di questa prova fa esattamente questo, e non è una forzatura:
// `net/http` stesso scrive messaggi della forma `dial tcp ...` e le librerie di
// terze parti citano l'URL con disinvoltura.
func TestIlSegretoNonEntraNelTestoDellErrore(t *testing.T) {
	t.Parallel()

	client := &http.Client{Transport: trasportoCheCitaLIndirizzo{}}
	exec := nuovoEsecutore(t, client, func(o *httpexec.Options) {
		o.Secrets = segretiDiProva(t, map[string]string{"DIGEST_TOKEN": valoreSegreto})
	})

	res, err := exec.Execute(t.Context(), occorrenza(t, jobSpec{
		URL: "https://api.example.com/digest?token=${DIGEST_TOKEN}",
	}))
	if err == nil {
		t.Fatal("Execute non ha restituito errore")
	}

	testo := res.ErrorText.String()
	if strings.Contains(testo, valoreSegreto) {
		t.Errorf("il testo dell'errore conserva la credenziale:\n  %s\n"+
			"Questo testo finisce in job_executions.error.", testo)
	}
	if res.ErrorText.Empty() {
		t.Error("l'errore è stato cancellato invece che redatto: chi legge il registro ha " +
			"bisogno di sapere che cosa è andato storto")
	}
	if !strings.Contains(testo, "${DIGEST_TOKEN}") {
		t.Errorf("il testo dell'errore non dice che cosa è stato tolto: %s", testo)
	}
	// E il caso sta esercitando ciò che dichiara: senza questo controllo, un
	// domani in cui il trasporto smettesse di citare l'indirizzo renderebbe le
	// verifiche qui sopra vere per vuoto.
	if !strings.Contains(err.Error(), valoreSegreto) {
		t.Fatalf("l'errore grezzo non contiene la credenziale: questo caso non prova niente: %v", err)
	}
}

// TestUnaRichiestaSenzaSegretiSegueLaStessaStrada è la sesta clausola del
// contratto: la maggioranza dei job non riferisce niente, e se il caso normale
// costasse di più il collegamento verrebbe aggirato proprio lì.
func TestUnaRichiestaSenzaSegretiSegueLaStessaStrada(t *testing.T) {
	t.Parallel()

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "ok")
	}))
	defer target.Close()

	exec := nuovoEsecutore(t, target.Client())
	res, err := exec.Execute(t.Context(), occorrenza(t, jobSpec{URL: target.URL}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := res.ResponseExcerpt.String(); got != "ok" {
		t.Errorf("estratto = %q, atteso %q: il redattore vuoto non deve toccare niente", got, "ok")
	}
}

// trasportoCheCitaLIndirizzo è un guasto di rete che nomina l'URL della
// richiesta, come fanno i messaggi di `net/http` e di parecchie librerie sotto
// di lui. Il client lo avvolgerà in un `*url.Error`: quell'involucro lo toglie
// [httpexec.requestError], e ciò che resta è questo messaggio.
type trasportoCheCitaLIndirizzo struct{}

func (trasportoCheCitaLIndirizzo) RoundTrip(req *http.Request) (*http.Response, error) {
	return nil, fmt.Errorf("dial tcp: lookup fallita per %s", req.URL.String())
}
