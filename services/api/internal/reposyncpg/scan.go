package reposyncpg

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/apdsoftware/postqron/services/api/internal/jobs"
	"github.com/apdsoftware/postqron/services/api/internal/reposync"
)

// scanRepository legge una riga di `repositories` nell'ordine di
// [repositoryColumns].
func scanRepository(row pgx.Row) (reposync.Repository, error) {
	var repo reposync.Repository
	var status string
	err := row.Scan(
		&repo.ID, &repo.UserID, &repo.InstallationID,
		&repo.Owner, &repo.Name, &repo.DefaultBranch, &repo.ConfigPath, &repo.Enabled,
		&repo.LastSyncedCommit, &status)
	if err != nil {
		return reposync.Repository{}, fmt.Errorf("reposyncpg: lettura del repository: %w", err)
	}
	repo.LastSyncStatus = reposync.SyncStatus(status)
	return repo, nil
}

// scanManagedJob legge una riga di `jobs` nell'ordine di [managedJobColumns].
func scanManagedJob(row pgx.Row) (jobs.Job, error) {
	var job jobs.Job
	var everySeconds int32
	var environments, channels []string
	var method, backoff, overlap string
	var headers []byte
	var timeoutSeconds int32
	var maxRetries int16
	var archivedAt *time.Time

	err := row.Scan(
		&job.ID, &job.UserID, &job.Name, &job.Description,
		&job.Schedule, &everySeconds, &job.Timezone, &environments,
		&job.URL, &method, &headers, &job.Body,
		&timeoutSeconds, &maxRetries, &backoff, &overlap, &channels,
		&job.Enabled, &archivedAt)
	if err != nil {
		return jobs.Job{}, fmt.Errorf("reposyncpg: lettura del job: %w", err)
	}

	job.Every = time.Duration(everySeconds) * time.Second
	job.Method = jobs.Method(method)
	job.RetryBackoff = jobs.Backoff(backoff)
	job.OverlapPolicy = jobs.OverlapPolicy(overlap)
	job.Timeout = time.Duration(timeoutSeconds) * time.Second
	job.MaxRetries = int(maxRetries)
	job.ArchivedAt = archivedAt

	job.Environments = make([]jobs.Environment, 0, len(environments))
	for _, env := range environments {
		job.Environments = append(job.Environments, jobs.Environment(env))
	}
	job.AlertOnFailure = make([]jobs.AlertChannel, 0, len(channels))
	for _, channel := range channels {
		job.AlertOnFailure = append(job.AlertOnFailure, jobs.AlertChannel(channel))
	}

	job.Headers = map[string]string{}
	if len(headers) > 0 {
		if err := json.Unmarshal(headers, &job.Headers); err != nil {
			return jobs.Job{}, fmt.Errorf("reposyncpg: header del job %q illeggibili: %w", job.Name, err)
		}
	}
	return job, nil
}

// ------------------------------------------------------------------ supporto
//
// Le colonne facoltative della 0005 hanno vincoli che il valore zero violerebbe
// (`every_seconds >= 1`, `schedule ~ '...'`): il valore assente si scrive NULL.

// overlapPolicy scrive la politica di sovrapposizione (R41), con il predefinito
// per chi non l'ha dichiarata. La colonna è NOT NULL e il parser di `cron.yaml`
// valorizza sempre il campo, ma il predefinito viene comunque dalla stessa
// costante di internal/jobs: due default che divergono farebbero comportare
// diversamente lo stesso file a seconda di chi lo scrive.
func overlapPolicy(p jobs.OverlapPolicy) string {
	if p == "" {
		return string(jobs.DefaultOverlapPolicy)
	}
	return string(p)
}

func nullable(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

// everySeconds traduce l'intervallo nella colonna, NULL se il job usa il cron.
// Il vincolo `jobs_schedule_xor_every_check` esige che esattamente una delle
// due modalità sia valorizzata.
func everySeconds(every time.Duration) *int32 {
	if every <= 0 {
		return nil
	}
	seconds := int32(every / time.Second)
	return &seconds
}

// nullCommit scrive NULL invece di un commit che
// `repositories_last_synced_commit_check` (0004) rifiuterebbe.
//
// Serve al solo percorso di fallimento: registrare *perché* il sync è fallito
// conta più che registrare *su quale commit*, e far cadere l'unica riga che
// spiega l'errore per un CHECK violato sarebbe il modo peggiore di perderla.
func nullCommit(commit string) *string {
	if !reposync.ValidCommit(commit) {
		return nil
	}
	return &commit
}

func nonNilHeaders(headers map[string]string) map[string]string {
	if headers == nil {
		return map[string]string{}
	}
	return headers
}

func stringsOf[T ~string](values []T) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, string(value))
	}
	return out
}
