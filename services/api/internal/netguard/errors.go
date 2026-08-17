package netguard

import "errors"

// ErrNotAllowed è **l'unico** errore che questo pacchetto restituisce quando
// rifiuta una destinazione, ed è deliberatamente povero.
//
// # Perché un solo errore, e senza dettagli
//
// Un messaggio che dicesse «169.254.169.254 è un indirizzo link-local» o anche
// solo «indirizzo interno rifiutato» risponderebbe, a chiunque abbia un account
// gratuito, alla domanda *«questo nome, dalla vostra rete, risolve
// internamente?»*. Ripetuta su un elenco di nomi — `redis.internal`,
// `db.staging`, `vault.corp` — quella risposta è una scansione della nostra
// rete fatta con la nostra API, gratis e senza pacchetti sospetti. Il rifiuto è
// una funzionalità; la **ragione** del rifiuto è informazione che non è
// dell'utente.
//
// Quindi: stesso testo per il loopback, per il link-local, per il metadata
// cloud, per la rete privata e per il prefisso aggiunto dal deployment. La
// ragione vera esiste, la produce [Policy.Allows] come stringa (non come
// `error`, così che non possa finire in una risposta HTTP per distrazione) e
// finisce nel log dell'operatore, dove serve davvero.
//
// # Cosa resta scoperto, e perché lo accettiamo
//
// Resta un bit: *accettato* contro *rifiutato*. Chi crea un job con un nome
// scelto da lui impara comunque se quel nome risolve a un indirizzo interno,
// anche senza sapere quale. Quel bit non è eliminabile — un blocco che non
// rifiuta non blocca — si può solo renderlo caro:
//
//   - la creazione di un job passa dalle quote per piano di R10 (#398): non è
//     un ciclo da diecimila nomi al secondo;
//   - il tetto per host di destinazione e il rilevamento di più account sullo
//     stesso bersaglio sono R39 (#456);
//   - la risoluzione fallita **non** è un rifiuto (vedi [Guard.CheckTarget]),
//     quindi il bit distingue «interno» da «pubblico *oppure* inesistente», non
//     da «pubblico».
//
// Lo stesso testo vale anche quando il rifiuto arriva in esecuzione, dal
// [Guard.DialContext]: il registro delle esecuzioni è visibile all'utente
// (R43), e due messaggi diversi nei due punti sarebbero lo stesso oracolo con
// un passaggio in più.
var ErrNotAllowed = errors.New(
	"destinazione non consentita: l'URL deve puntare a un host pubblico raggiungibile da internet, " +
		"e restare pubblico anche dopo gli eventuali redirect")

// ErrTooManyRedirects è il tetto di R40 applicato alla catena di redirect.
//
// Non è un errore di sicurezza — ogni salto viene comunque controllato — ma il
// limite oltre il quale una catena smette di essere un redirect e diventa un
// modo di far lavorare il nostro motore a spese nostre.
var ErrTooManyRedirects = errors.New("troppi redirect nella catena")
