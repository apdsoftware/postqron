// Package cronyaml legge il `cron.yaml` che sta nella radice del repository
// dell'utente e lo trasforma in job validati (R12, SPEC §9).
//
// # Che cosa è davvero questo package
//
// È la porta d'ingresso della promessa del prodotto. Postqron non vende
// «richieste HTTP a orario»: vende **schedulazioni che vivono come codice nel
// repository**, dove aggiungere un cronjob è una pull request, e la revisione di
// una modifica alla schedulazione è la stessa revisione di qualunque altra
// modifica. Tutto ciò che rende quella promessa credibile passa da qui, e in
// particolare una cosa sola: **la qualità degli errori**.
//
// Chi scrive questo file lo scrive in un editor, fa commit, fa push, e aspetta.
// Non ha un debugger, non vede il nostro codice, non può provare di nuovo senza
// lasciare una traccia nella cronologia del repository. Il messaggio che riceve
// è letteralmente tutto ciò che ha. Per questo ogni rifiuto di questo package
// dice **riga, colonna, campo e correzione** — vedi [Error] — e per questo li
// dice **tutti insieme**: tre job sbagliati sono un giro di correzione, non tre.
//
// # Che cosa valida, e contro chi
//
// Il parser non contiene una seconda copia delle regole del prodotto. Ogni
// verifica è delegata a chi la possiede già, perché due validatori che devono
// restare d'accordo prima o poi non lo sono più:
//
//   - la **forma del file** — chiavi, tipi, durate, versione — è di questo
//     package, ed è l'unica cosa che è sua;
//   - le **schedulazioni** sono di [schedule], attraverso [jobs.Job.Validate]:
//     l'esclusività fra `schedule` ed `every` (SPEC §9) è la stessa che il
//     database esprime con `jobs_schedule_xor_every_check`, e
//     TestIlVincoloDelDatabaseEQuelloDelParser lo prova leggendo la migrazione;
//   - i **segreti** sono di [secrets.NameSet.Validate], che è l'analisi di
//     `Resolve` senza decifratura e sulla stessa funzione di espansione: un
//     `${VAR}` che non esiste fallisce **qui, al sync**, e non alle tre di notte
//     durante l'esecuzione (R43);
//   - i **limiti di piano** sono di [jobs.Plan] (R15, SPEC §8): un `every: 1s`
//     su piano Free viene rifiutato dicendo quale piano lo consente, non
//     degradato in silenzio.
//
// # Il confine verso #423
//
// Questo package **non riconcilia niente**: non legge il database, non scrive
// job, non sa quali job esistano già. Restituisce un [File] — l'elenco dei job
// che il file descrive, ciascuno già validato e con la posizione da cui viene —
// e finisce lì. Creare, aggiornare e disattivare è la riconciliazione idempotente
// di R13, che è la issue #423 e riceve esattamente questo valore.
//
// Il confine a monte è [githubhook.PushSink]: da lì arriva la push verificata,
// da lì si scarica il file, e i byte scaricati entrano in [Parse].
//
// Due conseguenze di questo confine, che #423 deve conoscere:
//
//   - i [jobs.Job] restituiti hanno `RepositoryID` **vuoto**. È #423 a sapere a
//     quale riga di `repositories` la push appartiene, e a valorizzarlo;
//   - `Enabled` è sempre `true`. La pausa manuale di un job (`enabled = false`)
//     è una scelta dell'utente che la 0005 tiene deliberatamente distinta da
//     `archived_at` proprio perché deve **sopravvivere al sync**: il file non la
//     esprime e non deve poterla sovrascrivere.
package cronyaml

import (
	"github.com/apdsoftware/postqron/services/api/internal/jobs"
	"github.com/apdsoftware/postqron/services/api/internal/secrets"
)

// SupportedVersion è l'unica versione dello schema che questo parser conosce.
//
// `version` è obbligatorio (SPEC §9) e un valore diverso viene rifiutato invece
// che interpretato. È la ragione per cui il campo esiste: fra un anno lo schema
// potrà cambiare — una chiave che cambia significato, un default diverso — e i
// file già scritti continueranno a essere letti con le regole con cui sono stati
// scritti. Un parser che indovina la versione toglie da subito quella
// possibilità, perché rende impossibile distinguere un file vecchio da uno nuovo
// scritto male.
const SupportedVersion = 1

// Limiti di forma del file.
//
// Non sono limiti di piano: valgono per tutti, e non c'è un piano che li alza.
// Esistono perché la sorgente è un file dentro un repository qualsiasi, cioè un
// input che non controlliamo, e un parser senza tetti è una macchina che
// qualunque repository può fermare.
const (
	// MaxFileSize è la dimensione massima del `cron.yaml`. Un file di
	// schedulazioni scritto da una persona non ci arriva vicino: il `cron.yaml`
	// di SPEC §9 sta in mezzo chilobyte, e mille job ne occupano meno di
	// duecento.
	MaxFileSize = 256 << 10

	// MaxJobsPerFile è il numero massimo di voci in `jobs`. Il tetto vero al
	// numero di job è quello del piano ([jobs.Plan.CheckJobCount]); questo serve
	// solo a non far crescere senza limite il lavoro di una singola push, e sta
	// sopra la soglia di fair use più alta del listino (SPEC §8).
	MaxJobsPerFile = 1000
)

// Options è ciò che il parser deve sapere del workspace per cui sta leggendo.
//
// Sono tutte informazioni che vivono fuori dal file, e nessuna è deducibile dal
// file: è deliberato che vadano passate, perché un parser che se le procura da
// solo dovrebbe conoscere il database, e questo package non lo conosce.
type Options struct {
	// Source è il nome con cui il file compare negli errori. Vuoto vale
	// `cron.yaml`.
	Source string

	// Plan è il piano del workspace (R15, SPEC §8).
	//
	// Il valore zero vale [jobs.FreePlan], cioè il piano **più restrittivo**.
	// Non è una comodità: un piano assente che valesse «nessun limite»
	// trasformerebbe una dimenticanza del chiamante in un aggiramento silenzioso
	// del listino, e sarebbe l'unico tipo di difetto che nessun test nota
	// perché tutto continua a funzionare.
	Plan jobs.Plan

	// Secrets sono i nomi dei segreti vivi del workspace (R42), contro cui i
	// riferimenti `${VAR}` vengono verificati **senza decifrare niente**.
	//
	// L'insieme vuoto è legittimo e significa «questo workspace non ha
	// segreti»: un file che ne riferisce uno viene rifiutato, ed è la risposta
	// giusta. Chi chiama deve quindi passare l'insieme reale — non passarlo
	// significa dire al parser che non ce ne sono.
	Secrets secrets.NameSet

	// Guard è il controllo sui target ammessi (R38). nil salta il controllo.
	//
	// Vale la pena ricordare cosa questo controllo non è: la difesa vera è
	// l'apertura della connessione ([netguard.Guard.DialContext]), perché fra
	// il sync e l'esecuzione delle tre di notte il DNS può cambiare risposta
	// quante volte vuole. Qui serve ad anticipare la diagnosi, che è
	// esattamente ciò che questo package esiste per fare.
	Guard jobs.TargetGuard

	// OtherJobs è il numero di job che l'utente ha **fuori da questo file**:
	// quelli creati da dashboard e API, e quelli sincronizzati da altri
	// repository.
	//
	// Serve al tetto di piano sul numero di job (SPEC §8: 20 su Free, 200 su
	// Pro), che è un limite sull'utente e non sul file. Il conteggio non lo può
	// fare questo package — richiede il database, e richiede di sapere quali
	// job appartengono già a questo repository, che è la domanda della
	// riconciliazione (#423) — quindi arriva da fuori. Zero significa «questo
	// file è tutto ciò che l'utente ha».
	OtherJobs int
}

// withDefaults riempie le opzioni non valorizzate.
func (o Options) withDefaults() Options {
	if o.Source == "" {
		o.Source = "cron.yaml"
	}
	if o.Plan.Code == "" {
		o.Plan = jobs.FreePlan
	}
	return o
}

// Entry è una voce di `jobs`: il job di dominio, più il punto del file da cui
// viene.
//
// La posizione sopravvive alla validazione, e non è un residuo di lavorazione:
// serve alla riconciliazione (#423), che può fallire per ragioni che il parser
// non conosce — un nome già usato da un job creato a mano, un tetto di piano
// superato da un'altra scrittura concorrente — e che deve poterle riportare
// all'utente sulla riga giusta come fa questo package.
type Entry struct {
	// Job è il job come lo vede il dominio, con i default di `defaults` e quelli
	// di [jobs.NewJob] già applicati. `RepositoryID` è vuoto: lo valorizza #423.
	Job jobs.Job

	// Position è il punto in cui il job è dichiarato: la sua prima chiave.
	Position

	// Path è il percorso della voce nel file, `jobs[0]`. L'indice è quello
	// della sequenza, non il nome: due job possono avere lo stesso nome (ed è
	// un errore, ma va segnalato con due posizioni diverse).
	Path string
}

// File è un `cron.yaml` letto e valido.
//
// «Valido» è una parola forte e qui significa una cosa precisa: ogni job è
// passato da [jobs.Job.Validate] con il piano del workspace, i suoi `${VAR}`
// esistono davvero, e la sua schedulazione è stata costruita da [schedule].
// Un [File] restituito senza errore non contiene niente che il database possa
// rifiutare — è il senso di R13, «un file non valido non modifica lo stato
// esistente».
type File struct {
	// Source è il nome del file.
	Source string
	// Version è la versione dichiarata, sempre [SupportedVersion].
	Version int
	// Jobs sono le voci, **nell'ordine del file**. L'ordine non ha significato
	// per l'esecuzione, ma è quello in cui l'utente li ha scritti ed è quello in
	// cui vanno mostrati.
	Jobs []Entry
}

// Domain sono i job nella forma che la riconciliazione (#423) scrive.
func (f *File) Domain() []jobs.Job {
	out := make([]jobs.Job, 0, len(f.Jobs))
	for _, entry := range f.Jobs {
		out = append(out, entry.Job)
	}
	return out
}

// Names sono i nomi dei job, nell'ordine del file. Sono le chiavi su cui la
// riconciliazione decide se creare, aggiornare o disattivare (SPEC §9, R13).
func (f *File) Names() []string {
	out := make([]string, 0, len(f.Jobs))
	for _, entry := range f.Jobs {
		out = append(out, entry.Job.Name)
	}
	return out
}
