package reposync

import (
	"maps"
	"slices"

	"github.com/apdsoftware/postqron/services/api/internal/jobs"
)

// ChangeKind è il tipo di modifica che la riconciliazione richiede.
type ChangeKind string

const (
	// ChangeCreate è un job dichiarato dal file che il database non ha.
	ChangeCreate ChangeKind = "create"
	// ChangeUpdate è un job presente in entrambi e cambiato.
	ChangeUpdate ChangeKind = "update"
	// ChangeArchive è un job che il file non dichiara più.
	//
	// Non è una cancellazione, ed è la scelta più importante di questo package:
	// vedi il punto 1 della documentazione del package.
	ChangeArchive ChangeKind = "archive"
)

// Change è una modifica da applicare a un singolo job.
type Change struct {
	Kind ChangeKind

	// Job è il job nella forma da scrivere. Per [ChangeCreate] non ha ancora un
	// ID; per gli altri due porta quello del job esistente.
	Job jobs.Job

	// Restored distingue un aggiornamento che riporta in vita un job archiviato
	// da uno ordinario.
	//
	// Non cambia la scrittura — in entrambi i casi `archived_at` finisce a NULL
	// — ma cambia ciò che si racconta: «un job è tornato nel file» e «un job è
	// cambiato» sono due fatti diversi nel registro di un sync, e distinguerli
	// è ciò che permette di spiegare a posteriori perché un job ha ricominciato
	// a girare.
	Restored bool

	// ResetNextRun azzera `jobs.next_run_at` (0005, 0010).
	//
	// Quella colonna è dello scheduler e non si riscrive per abitudine: farlo
	// sarebbe un aggiornamento perso ogni volta che il motore la fa avanzare
	// fra la lettura e la scrittura. Si azzera solo quando la vecchia
	// occorrenza non significa più niente — cambio di schedulazione o di fuso —
	// e da lì il job ricade in `jobs_unscheduled_idx`, da cui il motore lo
	// riprende alla passata successiva.
	ResetNextRun bool
}

// Plan è tutto ciò che una riconciliazione deve scrivere.
//
// È un valore, non un'esecuzione: si calcola senza toccare niente, si può
// ispezionare, e solo dopo si applica. È ciò che rende provabile «un file non
// valido non tocca lo stato» — nel caso non valido un Plan non viene nemmeno
// costruito.
type Plan struct {
	// RepositoryID è il collegamento a cui il piano appartiene.
	RepositoryID string
	// UserID è il proprietario dei job.
	UserID string
	// Commit è il commit da cui il file è stato letto.
	Commit string

	// Changes sono le modifiche, nell'ordine in cui vanno applicate: prima le
	// archiviazioni, poi gli aggiornamenti, infine le creazioni. L'ordine non è
	// estetico — vedi [Reconcile].
	Changes []Change

	// Unchanged è il numero di job che il file descrive esattamente come sono
	// già. Su un push che non tocca `cron.yaml` sono tutti, ed è la prova che
	// la riconciliazione è idempotente.
	Unchanged int

	// MaxJobs è il tetto di piano al numero di job dell'utente, nil se il piano
	// non ne ha uno (SPEC §8: Team e Agency). Viaggia con il piano perché
	// [Store.Apply] deve poterlo riverificare **dentro** la transazione: fra il
	// conteggio e la scrittura l'utente può aver creato un job dalla dashboard.
	MaxJobs *int
}

// Empty indica un piano che non scrive niente sui job.
func (p Plan) Empty() bool { return len(p.Changes) == 0 }

// Counts conta le modifiche per tipo. Serve al log del sync, che è il posto in
// cui si spiega all'utente cosa è successo al suo push.
func (p Plan) Counts() (created, updated, archived int) {
	for _, change := range p.Changes {
		switch change.Kind {
		case ChangeCreate:
			created++
		case ChangeUpdate:
			updated++
		case ChangeArchive:
			archived++
		}
	}
	return created, updated, archived
}

// Applied è l'esito di un piano applicato.
type Applied struct {
	Created  int
	Updated  int
	Archived int
	// Restored è il sottoinsieme di Updated che era archiviato.
	Restored int
}

// Reconcile calcola cosa serve per portare i job del repository allo stato che
// il file descrive.
//
// `desired` sono i job del `cron.yaml` già validati (internal/cronyaml), con
// `RepositoryID` vuoto ed `Enabled` a true. `current` sono i job che il
// database ha per quel repository, **archiviati compresi**.
//
// # Cosa non viene mai scritto
//
// Tre campi di `current` non compaiono in nessun [Change] e non è una svista:
//
//   - `enabled` — è la pausa, che il file non esprime e che può venire tanto
//     dalla dashboard quanto da un downgrade di piano (R58); vedi il punto 2
//     della documentazione del package. Nemmeno un job appena ripristinato la
//     perde;
//   - `next_run_at` — è dello scheduler, e si azzera soltanto (vedi
//     [Change.ResetNextRun]);
//   - `created_at` — un job aggiornato è lo stesso job, e la sua età è un dato
//     dell'utente.
//
// # L'ordine delle modifiche
//
// Le archiviazioni vengono per prime e le creazioni per ultime. Il motivo è il
// tetto di piano: se un file toglie dieci job e ne aggiunge dieci su un piano
// al limite, applicare prima le creazioni lo sforerebbe di dieci e il sync
// fallirebbe su un file che invece ci sta.
func Reconcile(repo Repository, desired, current []jobs.Job) Plan {
	plan := Plan{RepositoryID: repo.ID, UserID: repo.UserID}

	existing := make(map[string]jobs.Job, len(current))
	for _, job := range current {
		existing[job.Name] = job
	}

	wanted := make(map[string]struct{}, len(desired))
	var updates, creates []Change

	for _, want := range desired {
		wanted[want.Name] = struct{}{}

		have, found := existing[want.Name]
		if !found {
			// Il job nasce con l'identità del repository e senza prossima
			// occorrenza: la calcola lo scheduler alla passata successiva
			// (0010).
			create := want
			create.RepositoryID = repo.ID
			create.UserID = repo.UserID
			create.NextRunAt = nil
			creates = append(creates, Change{Kind: ChangeCreate, Job: create})
			continue
		}

		restored := have.Archived()
		if sameSpec(have, want) && !restored {
			plan.Unchanged++
			continue
		}

		// Il job da scrivere è quello esistente con sopra i campi del file: è
		// così che `enabled`, l'id e la data di creazione restano quelli che
		// erano. Applicare il contrario — il job del file con sopra l'id —
		// significherebbe elencare qui, uno per uno, tutti i campi da salvare,
		// e dimenticarne uno il giorno in cui la tabella ne guadagna un altro.
		merged := have
		applySpec(&merged, want)

		updates = append(updates, Change{
			Kind:         ChangeUpdate,
			Job:          merged,
			Restored:     restored,
			ResetNextRun: mustResetNextRun(have, merged),
		})
	}

	var archives []Change
	for _, have := range current {
		if _, stillWanted := wanted[have.Name]; stillWanted {
			continue
		}
		if have.Archived() {
			// Già archiviato da un sync precedente: riarchiviarlo sposterebbe
			// `archived_at` in avanti a ogni push, e quella data è la risposta
			// alla domanda «da quando questo job non gira più».
			continue
		}
		archives = append(archives, Change{Kind: ChangeArchive, Job: have})
	}

	plan.Changes = make([]Change, 0, len(archives)+len(updates)+len(creates))
	plan.Changes = append(plan.Changes, archives...)
	plan.Changes = append(plan.Changes, updates...)
	plan.Changes = append(plan.Changes, creates...)
	return plan
}

// applySpec sovrascrive nel job esistente i soli campi che il file possiede.
//
// L'elenco è la definizione operativa di «cosa descrive `cron.yaml`»: ciò che
// non è qui è dell'utente o dello scheduler. Vedi SPEC §9 per il file e la
// migrazione 0005 per le colonne.
func applySpec(dst *jobs.Job, src jobs.Job) {
	dst.Description = src.Description
	dst.Schedule = src.Schedule
	dst.Every = src.Every
	dst.Timezone = src.Timezone
	dst.Environments = slices.Clone(src.Environments)
	dst.URL = src.URL
	dst.Method = src.Method
	dst.Headers = maps.Clone(src.Headers)
	dst.Body = src.Body
	dst.Timeout = src.Timeout
	dst.MaxRetries = src.MaxRetries
	dst.RetryBackoff = src.RetryBackoff
	dst.OverlapPolicy = src.OverlapPolicy
	dst.AlertOnFailure = slices.Clone(src.AlertOnFailure)

	// Un job che il file torna a dichiarare non è più archiviato. `Enabled`
	// resta quello che era: archiviare non è mai stata una risposta alla pausa,
	// e ripristinare non è una risposta alla pausa nemmeno adesso — vale sia per
	// una pausa dalla dashboard sia per una sospensione da downgrade (R58).
	dst.ArchivedAt = nil
}

// sameSpec indica che il job esistente descrive già ciò che il file dichiara.
//
// Confronta **solo** i campi di [applySpec]. È la funzione che rende
// idempotente il sync: due push identiche producono `false` da nessuna parte, e
// quindi zero scritture — non zero effetti *visibili*, proprio zero UPDATE.
// Contare `updated_at` fra i campi confrontati, o confrontare i job per intero,
// riscriverebbe ogni job a ogni push e renderebbe il registro delle modifiche
// una cronologia di eventi che non sono successi.
func sameSpec(have, want jobs.Job) bool {
	return have.Description == want.Description &&
		have.Schedule == want.Schedule &&
		have.Every == want.Every &&
		have.Timezone == want.Timezone &&
		slices.Equal(have.Environments, want.Environments) &&
		have.URL == want.URL &&
		have.Method == want.Method &&
		maps.Equal(have.Headers, want.Headers) &&
		have.Body == want.Body &&
		have.Timeout == want.Timeout &&
		have.MaxRetries == want.MaxRetries &&
		have.RetryBackoff == want.RetryBackoff &&
		have.OverlapPolicy == want.OverlapPolicy &&
		slices.Equal(have.AlertOnFailure, want.AlertOnFailure)
}

// mustResetNextRun ripete la regola di `jobs.Service`: la prossima occorrenza
// calcolata non vale più se il job non è più eseguibile o se la schedulazione è
// cambiata.
//
// È deliberatamente la stessa regola e deliberatamente scritta due volte: la
// versione di internal/jobs non è esportata, e questa issue non ha
// internal/jobs fra i propri percorsi. Se un giorno le due divergono, la
// seconda è questa — la nota è qui perché chi la legge sappia dove sta l'altra.
func mustResetNextRun(current, updated jobs.Job) bool {
	if !updated.Runnable() {
		return true
	}
	return current.Schedule != updated.Schedule ||
		current.Every != updated.Every ||
		current.Timezone != updated.Timezone
}
