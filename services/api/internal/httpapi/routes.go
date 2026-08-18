package httpapi

import (
	"net/http"

	"github.com/apdsoftware/postqron/services/api/internal/apikeys"
)

// router è tutto ciò di cui la registrazione delle rotte ha bisogno.
//
// È un'interfaccia e non `*http.ServeMux` per una ragione sola, ed è quella per
// cui esiste questo file: **il contratto OpenAPI (R51) deve poter essere
// confrontato con le rotte vere**. Un documento scritto a mano che nessuno
// confronta con il codice descrive il servizio finché qualcuno non aggiunge una
// rotta, e da quel momento descrive un servizio che non esiste — chi lo legge
// costruisce un client su una promessa falsa e il difetto si manifesta a casa
// sua.
//
// Con questa interfaccia l'elenco si ottiene passando a [register] un router che
// registra e basta, cioè **dalle stesse righe che governano l'esercizio** e
// dalle stesse condizioni: se una rotta è dietro un `if deps.X != nil`, l'elenco
// la contiene esattamente quando la contiene il servizio. La proiezione
// alternativa — un secondo elenco scritto a mano da confrontare col documento —
// avrebbe solo spostato la divergenza di un file.
type router interface {
	HandleFunc(pattern string, handler func(http.ResponseWriter, *http.Request))
}

// Il compilatore verifica che il mux vero soddisfi l'interfaccia: senza,
// scoprirlo richiederebbe di arrivare a NewRouter.
var _ router = (*http.ServeMux)(nil)

// scopedRoute è una rotta che una chiave API può usare, con il permesso che le
// serve (R9).
//
// Esiste come tabella e non come sequenza di chiamate perché lo scope di una
// rotta è un fatto del contratto quanto il suo percorso: è ciò che decide se una
// chiave di sola lettura può eseguire l'operazione. Scritto in una tabella, il
// documento OpenAPI lo può dichiarare e il controllo di allineamento lo può
// verificare; sparso in dieci chiamate, resterebbe verificabile solo leggendo.
type scopedRoute struct {
	// Pattern è il metodo e il percorso, nella forma di http.ServeMux.
	Pattern string
	// Scope è il permesso che una chiave API deve portare. Le sessioni passano da
	// tutte: gli scope limitano le deleghe, non il titolare (vedi
	// [Identity.Allows]).
	Scope   apikeys.Scope
	Handler identityHandler
}

// register registra le rotte della tabella, ciascuna dietro il proprio scope.
func (a *jobsAPI) register(mux router, routes []scopedRoute) {
	for _, route := range routes {
		mux.HandleFunc(route.Pattern, a.scoped(route.Scope, route.Handler))
	}
}
