// Package netguard rifiuta le destinazioni che non sono pubbliche (R38).
//
// Postqron esegue richieste HTTP verso URL scelti dall'utente **dalla stessa
// macchina su cui girano l'API e PostgreSQL** (SPEC §2, §3.7). Senza questo
// pacchetto un job che punta a `http://127.0.0.1:5433` parla con il nostro
// database, e uno che punta a `http://169.254.169.254` legge le credenziali
// della macchina dal servizio di metadata della piattaforma cloud. Non è una
// funzionalità mancante: è il prodotto che diventa uno strumento d'attacco,
// ed è il divieto che la [Acceptable Use Policy] §2.1 mette per iscritto.
//
// # Le tre cose che decidono se il blocco funziona
//
//  1. **Il controllo è sull'indirizzo risolto, non sul nome.** Un filtro sui
//     nomi si aggira registrando un dominio che punta a 127.0.0.1. Qui il nome
//     serve solo a ottenere degli indirizzi; è [Policy.Allows] a decidere, e
//     decide su un [netip.Addr].
//
//  2. **Si ripete su ogni connessione, quindi su ogni redirect.** Un bersaglio
//     pubblico che risponde `302 Location: http://169.254.169.254/` aggira
//     qualunque controllo fatto una volta sola all'inizio. Il controllo non sta
//     in `CheckRedirect` — sta nel [Guard.DialContext] del trasporto, che il
//     client HTTP attraversa per *ogni* salto: nessun hop può saltarlo, nemmeno
//     uno che aggiungessimo domani.
//
//  3. **Chi controlla e chi si connette vedono lo stesso indirizzo.** È il
//     punto in cui quasi tutte le implementazioni sbagliano: si risolve il
//     nome, si valida l'indirizzo, poi si passa *l'URL* a un client HTTP che
//     **risolve di nuovo** — e la seconda risoluzione può restituire un
//     indirizzo diverso (DNS rebinding). Qui la risoluzione avviene una volta
//     sola per connessione e si dialoga con **l'indirizzo letterale già
//     validato**: il nome non viene mai passato al dialer di sistema. Un terzo
//     controllo, in [net.Dialer.ControlContext], guarda l'indirizzo che il
//     socket sta davvero per contattare, subito prima della `connect(2)`.
//
// # Due punti di innesto, uno solo dei quali è la difesa
//
// [Guard.CheckTarget] soddisfa [jobs.TargetGuard] e serve a rifiutare un URL
// **al momento della creazione del job**: è comodità, non sicurezza. Non può
// esserlo: fra la validazione e l'esecuzione delle tre di notte passano ore, e
// il DNS in mezzo può cambiare quante volte vuole. La difesa è
// [Guard.DialContext], e vale nell'istante in cui la connessione si apre.
//
// Chi esegue i job (#389) deve usare [Guard.Client], non un `http.Client`
// costruito altrove: un client senza questo trasporto non è protetto da niente.
//
// # Cosa resta scoperto
//
// Il tetto alla frequenza per host di destinazione, il rilevamento di più
// account che puntano allo stesso bersaglio e la sospensione sono R39
// (issue #456): questo pacchetto dice *dove* non si può andare, non *quanto
// spesso* si può andare dove si può.
//
// [Acceptable Use Policy]: ../../../../legal/en/acceptable-use-policy.md
package netguard

import (
	"net/netip"
	"slices"
)

// Policy è l'elenco degli indirizzi verso cui non si esce.
//
// È deliberatamente una **lista di rifiuto e non di permesso**: lo spazio degli
// indirizzi pubblici è ciò che resta, e resta senza doverlo enumerare. Una lista
// di permesso qui vorrebbe dire elencare internet.
//
// Il valore zero è la policy predefinita e completa: `var p netguard.Policy`
// blocca già tutto ciò che va bloccato. Non esiste un modo di costruirne una
// permissiva per sbaglio.
type Policy struct {
	// Deny aggiunge prefissi al blocco predefinito, che resta comunque attivo.
	//
	// Serve al deployment: la VPS che ospita Postqron ha anche un indirizzo
	// *pubblico*, e su quell'indirizzo rispondono l'API e — se la porta è
	// esposta — PostgreSQL. Il blocco predefinito non può conoscerlo, perché non
	// è un indirizzo riservato: è un indirizzo pubblico che per noi è interno.
	// Metterlo qui è il modo di dire «e anche questo no».
	Deny []netip.Prefix

	// allow è la sola deroga possibile, e non è raggiungibile da fuori: i test
	// di questo pacchetto hanno bisogno di parlare con un `httptest.Server`, che
	// vive su 127.0.0.1. Vedi export_test.go.
	//
	// Non è esportata di proposito. Un'opzione pubblica che disattiva il
	// controllo diventa, nel giro di due refactor, l'opzione che qualcuno ha
	// impostato in produzione per far funzionare una prova.
	allow []netip.Prefix
}

// Allows dice se l'indirizzo è una destinazione ammessa.
//
// La seconda uscita è la **ragione del rifiuto, e non è testo per l'utente**:
// va nei log dell'operatore. Dire a chi ha creato il job «indirizzo di loopback
// rifiutato» gli conferma che quel nome risolve internamente, cioè gli regala
// la primitiva di scansione della nostra rete che questo pacchetto esiste per
// negare. La separazione è nella firma proprio perché non basta ricordarselo:
// la ragione non è un `error`, quindi non può finire in una risposta HTTP per
// distrazione. Vedi [ErrNotAllowed].
func (p Policy) Allows(addr netip.Addr) (bool, string) {
	if !addr.IsValid() {
		return false, "indirizzo non valido"
	}
	// Uno zone identifier (`fe80::1%eth0`) nomina un'interfaccia locale: è per
	// definizione una destinazione della macchina, non della rete. I prefissi
	// sotto lo prenderebbero comunque, ma il rifiuto esplicito evita di dover
	// dimostrare che li prendono *tutti*.
	if addr.Zone() != "" {
		return false, "indirizzo con zone identifier: è locale a un'interfaccia"
	}

	// Unmap è il punto in cui `::ffff:127.0.0.1` smette di essere un IPv6 e
	// torna a essere il 127.0.0.1 che è. Senza, un controllo scritto sui
	// prefissi IPv6 lo lascerebbe passare — ed è il modo classico di aggirare un
	// blocco che qualcuno ha scritto due volte, una per famiglia, invece che una
	// volta su una forma canonica.
	addr = addr.Unmap()

	for _, prefix := range p.allow {
		if prefix.Contains(addr) {
			return true, ""
		}
	}
	for _, prefix := range p.Deny {
		if prefix.Contains(addr) {
			return false, "prefisso vietato dalla configurazione: " + prefix.String()
		}
	}
	for _, blocked := range blockedRanges {
		if blocked.prefix.Contains(addr) {
			return false, blocked.reason
		}
	}
	return true, ""
}

// blockedRange è un prefisso e il motivo per cui non ci si va.
type blockedRange struct {
	prefix netip.Prefix
	reason string
}

// blockedRanges è la tabella del rifiuto, in IPv4 e IPv6.
//
// L'elenco è esplicito invece di appoggiarsi ai predicati della libreria
// standard ([netip.Addr.IsPrivate], [netip.Addr.IsLoopback] e simili) perché
// quelli coprono le categorie classiche e lasciano fuori tutto il resto:
// 100.64.0.0/10 (CGNAT) non è "privato" per net/netip, 198.18.0.0/15
// (benchmarking) non è niente di riconosciuto, e 2002::/16 è formalmente global
// unicast pur incapsulando un IPv4 arbitrario. Una tabella si legge in review;
// un `||` di sette predicati nasconde ciò che non contiene.
var blockedRanges = []blockedRange{
	// ------------------------------------------------------------------ IPv4
	{mustPrefix("0.0.0.0/8"), "questa rete (RFC 1122): comprende l'indirizzo non specificato"},
	{mustPrefix("10.0.0.0/8"), "rete privata (RFC 1918)"},
	{mustPrefix("100.64.0.0/10"), "spazio condiviso del CGNAT (RFC 6598)"},
	{mustPrefix("127.0.0.0/8"), "loopback: è la macchina su cui girano API e database"},
	{mustPrefix("169.254.0.0/16"), "link-local: comprende 169.254.169.254, il metadata della piattaforma cloud"},
	{mustPrefix("172.16.0.0/12"), "rete privata (RFC 1918)"},
	{mustPrefix("192.0.0.0/24"), "assegnazioni di protocollo IETF (RFC 6890)"},
	{mustPrefix("192.0.2.0/24"), "documentazione, TEST-NET-1 (RFC 5737)"},
	{mustPrefix("192.88.99.0/24"), "anycast dei relay 6to4, deprecato (RFC 7526)"},
	{mustPrefix("192.168.0.0/16"), "rete privata (RFC 1918)"},
	{mustPrefix("198.18.0.0/15"), "benchmarking (RFC 2544)"},
	{mustPrefix("198.51.100.0/24"), "documentazione, TEST-NET-2 (RFC 5737)"},
	{mustPrefix("203.0.113.0/24"), "documentazione, TEST-NET-3 (RFC 5737)"},
	{mustPrefix("224.0.0.0/4"), "multicast (RFC 5771)"},
	{mustPrefix("240.0.0.0/4"), "riservato: comprende il broadcast 255.255.255.255"},

	// ------------------------------------------------------------------ IPv6
	//
	// Nota: gli IPv4 mappati (`::ffff:0:0/96`) non compaiono qui perché
	// [Policy.Allows] li riporta a IPv4 prima di consultare la tabella, dove
	// finiscono nella riga giusta della sezione sopra. Elencarli qui darebbe una
	// seconda regola per lo stesso indirizzo, libera di divergere dalla prima.
	{mustPrefix("::/128"), "indirizzo non specificato"},
	{mustPrefix("::1/128"), "loopback: è la macchina su cui girano API e database"},
	{mustPrefix("::/96"), "IPv4-compatibile, deprecato (RFC 4291): incapsula un IPv4 arbitrario"},
	{mustPrefix("64:ff9b::/96"), "prefisso noto NAT64 (RFC 6052): incapsula un IPv4 arbitrario"},
	{mustPrefix("64:ff9b:1::/48"), "NAT64 a uso locale (RFC 8215)"},
	{mustPrefix("100::/64"), "discard-only (RFC 6666)"},
	{mustPrefix("2001::/32"), "Teredo (RFC 4380): incapsula un IPv4 arbitrario"},
	{mustPrefix("2001:10::/28"), "ORCHID, deprecato (RFC 4843)"},
	{mustPrefix("2001:20::/28"), "ORCHIDv2 (RFC 7343)"},
	{mustPrefix("2001:db8::/32"), "documentazione (RFC 3849)"},
	{mustPrefix("2002::/16"), "6to4 (RFC 3056): incapsula un IPv4 arbitrario"},
	{mustPrefix("3fff::/20"), "documentazione (RFC 9637)"},
	{mustPrefix("5f00::/16"), "segment routing, SRv6 (RFC 9602)"},
	{mustPrefix("fc00::/7"), "unique local (RFC 4193)"},
	{mustPrefix("fe80::/10"), "link-local (RFC 4291)"},
	{mustPrefix("ff00::/8"), "multicast (RFC 4291)"},
}

// mustPrefix è per le costanti della tabella: un prefisso scritto male è un
// errore di programmazione e va visto all'avvio, non alla prima richiesta che
// per caso ci passa vicino.
func mustPrefix(text string) netip.Prefix {
	prefix, err := netip.ParsePrefix(text)
	if err != nil {
		panic("netguard: prefisso non valido " + text + ": " + err.Error())
	}
	return prefix.Masked()
}

// clonePolicy serve a non condividere le slice con il chiamante: una policy
// costruita e poi modificata da fuori sarebbe un blocco che cambia sotto i piedi
// a un [Guard] già in uso.
func clonePolicy(p Policy) Policy {
	return Policy{Deny: slices.Clone(p.Deny), allow: slices.Clone(p.allow)}
}
