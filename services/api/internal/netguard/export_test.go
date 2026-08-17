package netguard

import (
	"context"
	"net/netip"
	"syscall"
)

// AllowForTest deroga al blocco per i prefissi indicati.
//
// Esiste per una necessità sola: i test di questo pacchetto devono parlare con
// un `httptest.Server`, che vive su 127.0.0.1 — cioè esattamente su ciò che il
// pacchetto ha il compito di rifiutare. Senza la deroga, l'unico modo di
// provare il blocco sui redirect sarebbe una rete vera, e la prova non sarebbe
// riproducibile.
//
// Sta in export_test.go e non nell'API pubblica di proposito: un'opzione
// esportata che disattiva un controllo di sicurezza finisce, prima o poi, in una
// configurazione di produzione scritta da chi voleva solo far passare una prova.
// Qui non è raggiungibile: il file esiste solo durante `go test` di questo
// pacchetto.
func AllowForTest(p Policy, prefixes ...netip.Prefix) Policy {
	p.allow = append(p.allow, prefixes...)
	return p
}

// ControlForTest espone il controllo che precede la `connect(2)`.
//
// È l'ultima rete di sicurezza di [Guard.DialContext] e, per costruzione, in
// esercizio non scatta mai: il codice sopra ha già rifiutato tutto ciò che
// rifiuterebbe. Un controllo che non scatta mai è un controllo che nessuno sa
// se funziona, ed è per questo che il test lo chiama a mano.
func ControlForTest(g *Guard, address string) error {
	return g.control(context.Background(), "tcp", address, syscall.RawConn(nil))
}
