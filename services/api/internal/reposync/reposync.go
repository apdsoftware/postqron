// Package reposync riconcilia i job di un repository con il suo `cron.yaml`
// (R13, SPEC §9).
//
// # Che cosa significa riconciliare
//
// Il file descrive lo **stato desiderato**, non un comando. Non dice «crea
// questi job»: dice «questi sono i job che devono esistere». La differenza si
// vede al secondo push, quando i job esistono già, e si vede soprattutto in ciò
// che il file *non* dice più — un job sparito dal file è una richiesta di
// smettere di eseguirlo, ed è una richiesta tanto esplicita quanto le altre.
//
// Da qui le tre operazioni di [Reconcile], e nessuna quarta:
//
//   - **creare** ciò che il file dichiara e il database non ha;
//   - **aggiornare** ciò che c'è in entrambi ma è cambiato;
//   - **archiviare** ciò che il database ha e il file non dichiara più.
//
// La chiave è `name` (SPEC §9). Rinominare un job non è un aggiornamento: è la
// cancellazione di un'identità e la nascita di un'altra, e la spec lo scrive
// perché è l'unica lettura che rende il file leggibile senza conoscere lo
// storico del repository.
//
// # Le tre cose che fanno danno
//
// Una riconciliazione ingenua è breve da scrivere e rovina i dati di qualcuno.
// I tre punti in cui succede sono questi, e ciascuno ha il proprio test:
//
//  1. **Archiviare non è cancellare.** Un job che sparisce dal file perde la
//     schedulazione e **tiene il proprio storico di esecuzioni**. È l'unico
//     modo in cui l'utente può ancora capire cosa faceva e come andava; una
//     DELETE porterebbe via le esecuzioni per cascata (0006), che è una
//     distruzione silenziosa di dati. La 0005 tiene `archived_at` distinto da
//     `enabled` esattamente per questo.
//
//  2. **La pausa sopravvive al sync.** `enabled = false` è una decisione che il
//     file non esprime: un push che non tocca quel job **non deve
//     riattivarlo**. È il vincolo che internal/cronyaml dichiara restituendo
//     sempre `Enabled: true`, e che qui diventa vero non scrivendo mai quella
//     colonna — né in un aggiornamento, né in un'archiviazione. Vale anche al
//     ritorno: un job messo in pausa, tolto dal file e poi rimesso torna in
//     pausa, perché archiviare non è mai stata una risposta alla pausa.
//
//     La pausa ha **due origini**, e la seconda rende la regola più importante
//     della prima. Una è la dashboard, dove l'utente ferma un job che sta
//     dando fastidio. L'altra è il downgrade di piano (R58): quando i job
//     attivi superano il tetto del piano di destinazione vengono sospesi
//     **tutti**, e sta all'utente riattivare quelli che gli servono. Un sync
//     che riaccendesse ciò che trova spento annullerebbe quella scelta in
//     blocco, al primo push, senza dire niente — e rimetterebbe l'account
//     sopra il tetto per cui non paga più.
//
//  3. **Un file non valido non tocca lo stato.** Gli errori tornano all'utente
//     — con riga e colonna, che è il lavoro di internal/cronyaml — e i job
//     restano quelli di prima. Un sync a metà è peggio di un sync mancato: il
//     mancato si ripete, quello a metà lascia un repository e un database che
//     dicono cose diverse senza che niente lo segnali.
//
// # Dove sta il confine
//
// Questo package non conosce pgx e non conosce HTTP. Riceve i byte del file da
// [Contents] — l'implementazione è internal/githubapp — li dà a
// [cronyaml.Parse], e passa il piano risultante a [Store], che è internal/reposyncpg.
// È ciò che permette di provare i tre casi qui sopra senza un database in piedi
// e senza una GitHub App installata da nessuna parte.
//
// A monte c'è [githubhook.PushSink], che [Service] implementa: è il confine che
// #421 aveva dichiarato e lasciato vuoto.
package reposync

import (
	"context"
	"errors"
	"regexp"

	"github.com/apdsoftware/postqron/services/api/internal/jobs"
	"github.com/apdsoftware/postqron/services/api/internal/secrets"
)

// Provider è il provider dei repository sincronizzati. Ce n'è uno solo (tipo
// `repository_provider` della 0001), e la costante esiste perché la query lo
// nomina.
const Provider = "github"

// SyncStatus è l'esito dell'ultima riconciliazione, uguale all'enumerato
// `sync_status` della migrazione 0001.
type SyncStatus string

const (
	// SyncPending è un sync cominciato e non concluso.
	SyncPending SyncStatus = "pending"
	// SyncSucceeded è un sync applicato per intero.
	SyncSucceeded SyncStatus = "succeeded"
	// SyncFailed è un sync rifiutato: il file non era valido, o non c'era. I
	// job restano quelli di prima.
	SyncFailed SyncStatus = "failed"
)

// Errori riconoscibili dal chiamante.
var (
	// ErrAlreadySynced indica che il commit era già stato riconciliato con
	// successo. Non è un fallimento: è l'idempotenza che ha funzionato.
	ErrAlreadySynced = errors.New("reposync: commit già sincronizzato")
)

// MaxSyncErrorLength è il tetto al testo dell'errore conservato in
// `repositories.last_sync_error`.
//
// Un file con mille job sbagliati produce mille righe di diagnostica, e quella
// colonna esiste per essere mostrata in dashboard: oltre una certa lunghezza
// non è più un messaggio, è un allegato. Il testo completo resta nei log della
// consegna.
const MaxSyncErrorLength = 8 << 10

// Repository è la riga di `repositories` su cui il sync lavora (0004).
type Repository struct {
	ID     string
	UserID string

	// InstallationID è l'installazione della GitHub App con cui leggere il
	// file. Zero significa che il collegamento non è mai stato completato: in
	// quel caso si usa quella che arriva con la push, che è comunque
	// autenticata dalla firma di R11.
	InstallationID int64

	Owner string
	Name  string

	// DefaultBranch è il ramo **che l'utente ha collegato**, non
	// necessariamente quello predefinito su GitHub. È la nostra
	// configurazione: una push su un altro ramo non sincronizza, ed è ciò che
	// rende possibile lavorare su un branch senza schedulare quello che
	// contiene.
	DefaultBranch string

	// ConfigPath è il percorso del file nel repository, `cron.yaml` per default.
	ConfigPath string

	// Enabled indica un collegamento attivo. Un repository disattivato riceve
	// comunque le push — il webhook è sull'installazione, non sul
	// collegamento — e non sincronizza.
	Enabled bool

	// LastSyncedCommit e LastSyncStatus sono l'esito dell'ultima
	// riconciliazione. Insieme sono la memoria su cui poggia l'idempotenza:
	// vedi [Service.HandlePush].
	LastSyncedCommit string
	LastSyncStatus   SyncStatus
}

// FullName è `owner/name`, la forma con cui il repository compare nei log.
func (r Repository) FullName() string { return r.Owner + "/" + r.Name }

// commitFormat è la forma che `repositories_last_synced_commit_check` (0004)
// accetta. Serve a non far scoprire al database un valore che non gli
// andrebbe: un CHECK violato sarebbe un 500 su una consegna che GitHub
// ripeterebbe all'infinito.
var commitFormat = regexp.MustCompile(`^[0-9a-f]{7,40}$`)

// ValidCommit indica se il commit è scrivibile in `repositories`.
func ValidCommit(commit string) bool { return commitFormat.MatchString(commit) }

// Contents legge un file da un repository. L'implementazione è
// [githubapp.Client].
//
// Il secondo valore di ritorno è **«il file c'è»**, e non un errore, perché un
// `cron.yaml` assente non è un guasto: è uno stato del repository su cui questo
// package prende una decisione. Vedi [Service.sync].
type Contents interface {
	FileAtRef(ctx context.Context, installationID int64, owner, repo, path, ref string) ([]byte, bool, error)
}

// Plans legge il piano di un utente (R15). L'implementazione è
// [jobspg.Store]: la query esiste già e duplicarla creerebbe due listini.
type Plans interface {
	PlanForUser(ctx context.Context, userID string) (jobs.Plan, error)
}

// SecretNames elenca i nomi dei segreti vivi di un workspace (R42).
// L'implementazione è [secrets.Service].
//
// Serve al parser, che verifica i `${VAR}` del file **senza decifrare niente**:
// un riferimento inesistente fa fallire il sync qui, non l'esecuzione alle tre
// di notte.
type SecretNames interface {
	Available(ctx context.Context, userID string) (secrets.NameSet, error)
}

// Store è la persistenza della riconciliazione. L'implementazione PostgreSQL è
// internal/reposyncpg.
type Store interface {
	// RepositoriesByExternalID trova i collegamenti che seguono un repository.
	//
	// Sono più d'uno per costruzione: `repositories_identity_key` è unico per
	// utente, quindi due clienti che collegano lo stesso repository pubblico
	// hanno due righe, e ciascuna va riconciliata con il proprio piano e i
	// propri segreti.
	RepositoriesByExternalID(ctx context.Context, externalID int64) ([]Repository, error)

	// ManagedJobs elenca i job del repository, **archiviati compresi**.
	//
	// Gli archiviati servono: un job tolto dal file e poi rimesso deve tornare
	// quello di prima, non un secondo job con lo stesso nome. Il vincolo
	// `jobs_repository_name_key` (0005) non esclude gli archiviati, quindi
	// crearne uno nuovo fallirebbe comunque — ma fallirebbe con una violazione
	// di unicità invece che con il comportamento giusto.
	ManagedJobs(ctx context.Context, repositoryID string) ([]jobs.Job, error)

	// CountJobsOutside conta i job non archiviati dell'utente che **non**
	// appartengono a questo repository.
	//
	// È ciò che [cronyaml.Options.OtherJobs] chiede: il tetto di piano è
	// sull'utente, non sul file, e i job di questo repository stanno per essere
	// sostituiti da quelli del file.
	CountJobsOutside(ctx context.Context, userID, repositoryID string) (int, error)

	// Apply applica il piano e registra l'esito, **in una sola transazione**.
	//
	// L'atomicità è il senso di R13: creare metà dei job e fallire sull'altra
	// metà lascerebbe un repository e un database che descrivono due prodotti
	// diversi. Restituisce [ErrAlreadySynced] se il commit era già stato
	// applicato — la verifica va rifatta qui dentro, sotto il lock della riga,
	// perché due consegne diverse dello stesso commit possono arrivare insieme.
	Apply(ctx context.Context, plan Plan) (Applied, error)

	// RecordFailure registra un sync fallito **senza toccare i job**.
	RecordFailure(ctx context.Context, repositoryID, commit, reason string) error
}
