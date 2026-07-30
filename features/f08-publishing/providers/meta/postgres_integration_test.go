package meta

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	publishing "github.com/apdsoftware/postqron/features/f08-publishing"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestPostgresNotificationStoreIsIdempotentUnderRace(t *testing.T) {
	databaseURL := os.Getenv("F08_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("F08_DATABASE_URL is not configured")
	}
	database, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC()
	store, err := NewPostgresNotificationStore(database, func() time.Time {
		return now
	})
	if err != nil {
		t.Fatal(err)
	}
	suffix := now.Format("20060102150405.000000000")
	key := "meta-notification-integration-" + suffix
	t.Cleanup(func() {
		_, _ = database.Exec(
			`DELETE FROM f08_meta_notification_outbox
			  WHERE provider = 'facebook_groups' AND idempotency_key = $1`,
			key,
		)
	})

	const workers = 16
	results := make(chan string, workers)
	failures := make(chan error, workers)
	var wait sync.WaitGroup
	for range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			id, putErr := store.PutIfAbsent(
				context.Background(),
				"facebook_groups",
				"workspace-integration",
				key,
				json.RawMessage(`{"format":"text","text":"publish manually"}`),
			)
			if putErr != nil {
				failures <- putErr
				return
			}
			results <- id
		}()
	}
	wait.Wait()
	close(results)
	close(failures)
	for failure := range failures {
		t.Error(failure)
	}
	var expected string
	for id := range results {
		if expected == "" {
			expected = id
		}
		if id != expected {
			t.Fatalf("delivery IDs differ: %q != %q", id, expected)
		}
	}

	_, err = store.PutIfAbsent(
		context.Background(),
		"facebook_groups",
		"workspace-integration",
		key,
		json.RawMessage(`{"format":"text","text":"different payload"}`),
	)
	if !errors.Is(err, publishing.ErrConflict) {
		t.Fatalf("idempotency conflict error=%v", err)
	}
}
