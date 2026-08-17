package netguard_test

import (
	"net/netip"
	"strings"
	"testing"

	"github.com/apdsoftware/postqron/services/api/internal/netguard"
)

// TestIndirizziRifiutati elenca ciò da cui R38 deve proteggere.
//
// L'elenco è scritto per esteso e non generato dai prefissi del pacchetto: un
// test che rileggesse la stessa tabella del codice non proverebbe niente,
// direbbe solo che la tabella è uguale a sé stessa.
func TestIndirizziRifiutati(t *testing.T) {
	casi := []struct {
		addr   string
		motivo string
	}{
		// Il caso che dà il nome alla issue: PostgreSQL sta sulla stessa
		// macchina, sulla 5433 (vedi AGENTS.md §7).
		{"127.0.0.1", "loopback: è il database di Postqron"},
		{"127.0.0.53", "tutto 127.0.0.0/8, non il solo 127.0.0.1"},
		{"::1", "loopback IPv6"},
		{"0.0.0.0", "indirizzo non specificato"},
		{"::", "indirizzo non specificato IPv6"},

		// Metadata delle piattaforme cloud: credenziali della macchina a chi
		// sappia fare una GET.
		{"169.254.169.254", "metadata cloud"},
		{"169.254.0.1", "link-local, non il solo indirizzo di metadata"},
		{"fe80::1", "link-local IPv6"},

		// Reti private.
		{"10.0.0.1", "RFC 1918"},
		{"10.255.255.255", "estremo alto di 10/8"},
		{"172.16.0.1", "estremo basso di 172.16/12"},
		{"172.31.255.255", "estremo alto di 172.16/12"},
		{"192.168.1.1", "RFC 1918"},
		{"fd00::1", "unique local"},
		{"fc00::1", "unique local, estremo basso"},

		// Il resto dello spazio non pubblico, che i predicati della libreria
		// standard non coprono.
		{"100.64.0.1", "CGNAT"},
		{"198.18.0.1", "benchmarking"},
		{"192.0.2.1", "TEST-NET-1"},
		{"198.51.100.1", "TEST-NET-2"},
		{"203.0.113.1", "TEST-NET-3"},
		{"192.0.0.1", "assegnazioni IETF"},
		{"192.88.99.1", "relay 6to4"},
		{"224.0.0.1", "multicast"},
		{"239.255.255.250", "multicast, SSDP"},
		{"240.0.0.1", "riservato"},
		{"255.255.255.255", "broadcast"},
		{"ff02::1", "multicast IPv6"},
		{"2001:db8::1", "documentazione"},
		{"3fff::1", "documentazione (RFC 9637)"},
		{"100::1", "discard-only"},
		{"5f00::1", "SRv6"},

		// I meccanismi di transizione: sono IPv6 formalmente global unicast e
		// incapsulano un IPv4 che non controlliamo.
		{"2002:7f00:1::1", "6to4 con 127.0.0.1 dentro"},
		{"2001::1", "Teredo"},
		{"64:ff9b::7f00:1", "NAT64 verso 127.0.0.1"},
		{"::7f00:1", "IPv4-compatibile deprecato, 127.0.0.1"},
		{"2001:10::1", "ORCHID"},
		{"2001:20::1", "ORCHIDv2"},

		// Gli IPv4 mappati in IPv6: il modo classico di aggirare un blocco
		// scritto due volte, una per famiglia.
		{"::ffff:127.0.0.1", "loopback mappato"},
		{"::ffff:169.254.169.254", "metadata cloud mappato"},
		{"::ffff:10.0.0.1", "rete privata mappata"},
		{"::ffff:192.168.0.1", "rete privata mappata"},
		{"::ffff:7f00:1", "loopback mappato in forma esadecimale"},
	}

	var p netguard.Policy
	for _, c := range casi {
		t.Run(c.addr, func(t *testing.T) {
			addr := netip.MustParseAddr(c.addr)
			ok, reason := p.Allows(addr)
			if ok {
				t.Fatalf("%s è stato ammesso (%s): il motore potrebbe raggiungerlo", c.addr, c.motivo)
			}
			if strings.TrimSpace(reason) == "" {
				t.Errorf("%s rifiutato senza ragione: il log dell'operatore resterebbe muto", c.addr)
			}
		})
	}
}

// TestIndirizziAmmessi verifica che il blocco non chiuda internet.
//
// Un blocco che rifiuta tutto passa il test precedente e non serve a niente: i
// confini si provano da entrambi i lati.
func TestIndirizziAmmessi(t *testing.T) {
	casi := []string{
		"8.8.8.8",
		"1.1.1.1",
		"93.184.215.14",
		"9.255.255.255",   // subito sotto 10/8
		"11.0.0.0",        // subito sopra 10/8
		"172.15.255.255",  // subito sotto 172.16/12
		"172.32.0.0",      // subito sopra 172.16/12
		"192.167.255.255", // subito sotto 192.168/16
		"192.169.0.0",     // subito sopra 192.168/16
		"100.63.255.255",  // subito sotto il CGNAT
		"100.128.0.0",     // subito sopra il CGNAT
		"223.255.255.255", // subito sotto il multicast
		"2606:4700::1111",
		"2a00:1450:4001:80f::200e",
		"::ffff:8.8.8.8", // un IPv4 pubblico mappato resta pubblico
	}

	var p netguard.Policy
	for _, testo := range casi {
		t.Run(testo, func(t *testing.T) {
			if ok, reason := p.Allows(netip.MustParseAddr(testo)); !ok {
				t.Fatalf("%s è stato rifiutato (%s): il blocco sta chiudendo internet", testo, reason)
			}
		})
	}
}

// TestZoneIdentifierRifiutato copre `fe80::1%eth0`: nomina un'interfaccia della
// macchina, quindi non è una destinazione di rete.
func TestZoneIdentifierRifiutato(t *testing.T) {
	var p netguard.Policy
	if ok, _ := p.Allows(netip.MustParseAddr("fe80::1%eth0")); ok {
		t.Fatal("un indirizzo con zone identifier è stato ammesso")
	}
}

// TestIndirizzoNonValidoRifiutato: il valore zero di netip.Addr non è una
// destinazione, e non deve cadere nel ramo «nessun prefisso lo contiene, quindi
// va bene».
func TestIndirizzoNonValidoRifiutato(t *testing.T) {
	var p netguard.Policy
	if ok, _ := p.Allows(netip.Addr{}); ok {
		t.Fatal("l'indirizzo zero è stato ammesso")
	}
}

// TestDenyAggiuntivo è il caso del deployment: la VPS che ospita Postqron ha un
// indirizzo pubblico su cui rispondono l'API e, se la porta è esposta, il
// database. È pubblico per il mondo e interno per noi, e il blocco predefinito
// non può saperlo.
func TestDenyAggiuntivo(t *testing.T) {
	p := netguard.Policy{Deny: []netip.Prefix{netip.MustParsePrefix("203.0.114.7/32")}}

	if ok, _ := p.Allows(netip.MustParseAddr("203.0.114.7")); ok {
		t.Fatal("il prefisso aggiunto dalla configurazione non è stato applicato")
	}
	if ok, _ := p.Allows(netip.MustParseAddr("203.0.114.8")); !ok {
		t.Fatal("il prefisso aggiunto ha bloccato più di quanto dichiarasse")
	}
	// E il blocco predefinito resta attivo accanto a quello configurato.
	if ok, _ := p.Allows(netip.MustParseAddr("127.0.0.1")); ok {
		t.Fatal("valorizzare Deny ha sostituito il blocco predefinito invece di aggiungersi")
	}
}
