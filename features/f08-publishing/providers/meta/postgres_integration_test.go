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
	t.Cleanup(func() { _ = database.Close() })
	now := time.Now().UTC()
	store, err := NewPostgresNotificationStore(database, func() time.Time {
		return now
	})
	if err != nil {
		t.Fatal(err)
	}
	suffix := now.Format("20060102150405.000000000")
	key := "meta-notification-integration-" + suffix
	accountID := "account-meta-notification-" + suffix
	workspaceID := "workspace-meta-notification-" + suffix
	_, err = database.Exec(`
		INSERT INTO auth_accounts (
			id, email, normalized_email, display_name, contract_country,
			created_at, email_verified_at
		) VALUES ($1, $2, $2, 'Meta notification owner', 'IT', $3, $3)`,
		accountID,
		"meta-notification-"+suffix+"@example.test",
		now,
	)
	if err == nil {
		_, err = database.Exec(`
		INSERT INTO f04_workspaces (
			id, personal_account_id, name, status, created_at, updated_at
		) VALUES ($1, $2, 'Meta notification workspace', 'active', $3, $3)`,
			workspaceID,
			accountID,
			now,
		)
	}
	if err == nil {
		_, err = database.Exec(`
		INSERT INTO f04_memberships (
			workspace_id, account_id, role, status, created_at, updated_at
		) VALUES ($1, $2, 'owner', 'active', $3, $3)`,
			workspaceID,
			accountID,
			now,
		)
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = database.Exec(
			`DELETE FROM f08_meta_notification_outbox
			  WHERE provider = 'facebook_groups' AND idempotency_key = $1`,
			key,
		)
		_, _ = database.Exec(`DELETE FROM f04_workspaces WHERE id = $1`, workspaceID)
		_, _ = database.Exec(`DELETE FROM auth_accounts WHERE id = $1`, accountID)
	})

	const workers = 16
	results := make(chan string, workers)
	failures := make(chan error, workers)
	var wait sync.WaitGroup
	for range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			id, delivered, putErr := store.PutIfAbsent(
				context.Background(),
				"facebook_groups",
				workspaceID,
				"post-integration",
				"channel-integration",
				key,
				json.RawMessage(`{"format":"text","text":"publish manually"}`),
			)
			if putErr != nil {
				failures <- putErr
				return
			}
			if delivered {
				failures <- errors.New("new notification unexpectedly delivered")
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

	_, _, err = store.PutIfAbsent(
		context.Background(),
		"facebook_groups",
		workspaceID,
		"post-integration",
		"channel-integration",
		key,
		json.RawMessage(`{"format":"text","text":"different payload"}`),
	)
	if !errors.Is(err, publishing.ErrConflict) {
		t.Fatalf("idempotency conflict error=%v", err)
	}
}

type metaDeliverySender struct {
	mu         sync.Mutex
	deliveries map[string]NotificationDelivery
}

func (sender *metaDeliverySender) DeliverMetaNotification(
	_ context.Context,
	delivery NotificationDelivery,
) error {
	sender.mu.Lock()
	defer sender.mu.Unlock()
	if sender.deliveries == nil {
		sender.deliveries = make(map[string]NotificationDelivery)
	}
	if existing, ok := sender.deliveries[delivery.IdempotencyKey]; ok &&
		existing.ID != delivery.ID {
		return errors.New("idempotency collision")
	}
	sender.deliveries[delivery.IdempotencyKey] = delivery
	return nil
}

func TestPostgresNotificationDispatcherClaimsAndDeliversIdempotently(
	t *testing.T,
) {
	databaseURL := os.Getenv("F08_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("F08_DATABASE_URL is not configured")
	}
	database, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	now := time.Now().UTC()
	store, err := NewPostgresNotificationStore(database, func() time.Time {
		return now
	})
	if err != nil {
		t.Fatal(err)
	}
	suffix := now.Format("20060102150405.000000000")
	accountID := "account-meta-dispatch-" + suffix
	workspaceID := "workspace-meta-dispatch-" + suffix
	key := "meta-dispatch-" + suffix
	_, err = database.Exec(`
		INSERT INTO auth_accounts (
			id, email, normalized_email, display_name, contract_country,
			created_at, email_verified_at
		) VALUES ($1, $2, $2, 'Meta dispatch owner', 'IT', $3, $3)`,
		accountID,
		"meta-dispatch-"+suffix+"@example.test",
		now,
	)
	if err == nil {
		_, err = database.Exec(`
		INSERT INTO f04_workspaces (
			id, personal_account_id, name, status, created_at, updated_at
		) VALUES ($1, $2, 'Meta dispatch workspace', 'active', $3, $3)`,
			workspaceID,
			accountID,
			now,
		)
	}
	if err == nil {
		_, err = database.Exec(`
		INSERT INTO f04_memberships (
			workspace_id, account_id, role, status, created_at, updated_at
		) VALUES ($1, $2, 'owner', 'active', $3, $3)`,
			workspaceID,
			accountID,
			now,
		)
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = database.Exec(
			`DELETE FROM f08_meta_notification_outbox WHERE idempotency_key = $1`,
			key,
		)
		_, _ = database.Exec(`DELETE FROM f04_workspaces WHERE id = $1`, workspaceID)
		_, _ = database.Exec(`DELETE FROM auth_accounts WHERE id = $1`, accountID)
	})
	id, delivered, err := store.PutIfAbsent(
		context.Background(),
		"instagram_personal",
		workspaceID,
		"post-dispatch",
		"channel-dispatch",
		key,
		json.RawMessage(`{"format":"image","media":[{"url":"https://media.example/1.jpg"}]}`),
	)
	if err != nil || delivered {
		t.Fatalf("enqueue id=%q delivered=%v error=%v", id, delivered, err)
	}
	sender := &metaDeliverySender{}
	dispatcher, err := NewNotificationDispatcher(store, sender, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	processed, err := dispatcher.DispatchOne(context.Background())
	if err != nil || !processed {
		t.Fatalf("dispatch processed=%v error=%v", processed, err)
	}
	var persistedState string
	if err = database.QueryRow(
		`SELECT state FROM f08_meta_notification_outbox WHERE id = $1`,
		id,
	).Scan(&persistedState); err != nil {
		t.Fatal(err)
	}
	replayedID, delivered, err := store.PutIfAbsent(
		context.Background(),
		"instagram_personal",
		workspaceID,
		"post-dispatch",
		"channel-dispatch",
		key,
		json.RawMessage(`{"format":"image","media":[{"url":"https://media.example/1.jpg"}]}`),
	)
	if err != nil || !delivered || replayedID != id {
		t.Fatalf(
			"replay id=%q delivered=%v state=%q error=%v",
			replayedID,
			delivered,
			persistedState,
			err,
		)
	}
	sender.mu.Lock()
	defer sender.mu.Unlock()
	record := sender.deliveries[key]
	if len(sender.deliveries) != 1 || record.PostID != "post-dispatch" ||
		record.ChannelID != "channel-dispatch" || record.RecipientID != accountID {
		t.Fatalf("deliveries=%+v", sender.deliveries)
	}
}
