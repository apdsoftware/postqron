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

type crashInjectedEmailBoundary struct {
	mu        sync.Mutex
	emailIDs  map[string]string
	calls     int
	confirmed bool
}

type notificationSenderFunc func(
	context.Context,
	NotificationDelivery,
) (string, error)

func (function notificationSenderFunc) DeliverMetaNotification(
	ctx context.Context,
	delivery NotificationDelivery,
) (string, error) {
	return function(ctx, delivery)
}

func (sender *crashInjectedEmailBoundary) DeliverMetaNotification(
	_ context.Context,
	delivery NotificationDelivery,
) (string, error) {
	sender.mu.Lock()
	defer sender.mu.Unlock()
	if sender.emailIDs == nil {
		sender.emailIDs = make(map[string]string)
	}
	sender.calls++
	emailID := "email_crash_" + delivery.ID
	if existing := sender.emailIDs[delivery.IdempotencyKey]; existing != "" &&
		existing != emailID {
		return "", errors.New("downstream idempotency collision")
	}
	sender.emailIDs[delivery.IdempotencyKey] = emailID
	if !sender.confirmed {
		return emailID, &publishing.ProviderError{
			Code:      "email_pending",
			Detail:    "delivery confirmation pending",
			Retryable: true,
		}
	}
	return emailID, nil
}

func (sender *metaDeliverySender) DeliverMetaNotification(
	_ context.Context,
	delivery NotificationDelivery,
) (string, error) {
	sender.mu.Lock()
	defer sender.mu.Unlock()
	if sender.deliveries == nil {
		sender.deliveries = make(map[string]NotificationDelivery)
	}
	if existing, ok := sender.deliveries[delivery.IdempotencyKey]; ok &&
		existing.ID != delivery.ID {
		return "", errors.New("idempotency collision")
	}
	sender.deliveries[delivery.IdempotencyKey] = delivery
	return "", nil
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
		_, _ = database.Exec(
			`DELETE FROM f14_email_deliveries WHERE id = $1`,
			"email_crash_"+StableNotificationID("facebook_groups", key),
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

func TestPostgresNotificationCrashAfterDownstreamEnqueueDoesNotSendTwice(
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
	accountID := "account-meta-crash-" + suffix
	workspaceID := "workspace-meta-crash-" + suffix
	key := "meta-crash-" + suffix
	permanentKey := key + "-permanent"
	_, err = database.Exec(`
		INSERT INTO auth_accounts (
			id, email, normalized_email, display_name, contract_country,
			created_at, email_verified_at
		) VALUES ($1, $2, $2, 'Meta crash owner', 'IT', $3, $3)`,
		accountID,
		"meta-crash-"+suffix+"@example.test",
		now,
	)
	if err == nil {
		_, err = database.Exec(`
		INSERT INTO account_privacy_profiles (
			account_id, display_name, locale, timezone, updated_at
		) VALUES ($1, 'Meta crash owner', 'fr-FR', 'Europe/Paris', $2)`,
			accountID,
			now,
		)
	}
	if err == nil {
		_, err = database.Exec(`
		INSERT INTO f04_workspaces (
			id, personal_account_id, name, status, created_at, updated_at
		) VALUES ($1, $2, 'Meta crash workspace', 'active', $3, $3)`,
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
			  WHERE idempotency_key IN ($1, $2)`,
			key,
			permanentKey,
		)
		_, _ = database.Exec(`DELETE FROM f04_workspaces WHERE id = $1`, workspaceID)
		_, _ = database.Exec(
			`DELETE FROM account_privacy_profiles WHERE account_id = $1`,
			accountID,
		)
		_, _ = database.Exec(`DELETE FROM auth_accounts WHERE id = $1`, accountID)
	})
	id, delivered, err := store.PutIfAbsent(
		context.Background(),
		"facebook_groups",
		workspaceID,
		"post-crash",
		"channel-crash",
		key,
		json.RawMessage(`{"format":"text","text":"never persist this body"}`),
	)
	if err != nil || delivered {
		t.Fatalf("enqueue id=%q delivered=%v error=%v", id, delivered, err)
	}
	claimed, found, err := store.ClaimDue(
		context.Background(),
		now,
		now.Add(time.Minute),
		"lease_crash_injection",
	)
	if err != nil || !found {
		t.Fatalf("initial claim = %+v, %v, %v", claimed, found, err)
	}
	if claimed.RecipientID != accountID || claimed.Locale != "fr" ||
		claimed.TemplateID != "facebook_group_manual_publish" {
		t.Fatalf("server-resolved command = %+v", claimed)
	}
	sender := &crashInjectedEmailBoundary{}
	firstEmailID, err := sender.DeliverMetaNotification(
		context.Background(),
		claimed,
	)
	if err == nil || firstEmailID == "" {
		t.Fatalf("crash injection downstream result = %q, %v", firstEmailID, err)
	}
	_, err = database.Exec(`
		INSERT INTO f14_email_deliveries (
			id, idempotency_key, channel, template_id, template_version,
			recipient_id, recipient_email, recipient_name, subject, preheader,
			html_body, text_body, locale, state, attempt_count, max_attempts,
			next_attempt_at, created_at, updated_at
		) VALUES (
			$1, $2, 'transactional', 'facebook_group_manual_publish', '1.0.0',
			$3, $4, 'Meta crash owner', 'Manual action', 'Manual action',
			'<p>Manual action</p>', 'Manual action', 'fr', 'delivered', 1, 5,
			$5, $5, $5
		)`,
		firstEmailID,
		"social-notification:"+key,
		accountID,
		"meta-crash-"+suffix+"@example.test",
		now,
	)
	if err != nil {
		t.Fatal(err)
	}
	// Crash injection: do not record the email ID and do not release the F8
	// lease. The next worker must reclaim it without creating a second email.
	now = now.Add(2 * time.Minute)
	sender.confirmed = true
	dispatcher, err := NewNotificationDispatcher(store, sender, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	processed, err := dispatcher.DispatchOne(context.Background())
	if err != nil || !processed {
		t.Fatalf("reclaimed dispatch = %v, %v", processed, err)
	}
	var (
		state          string
		storedEmailID  string
		retentionUntil time.Time
	)
	err = database.QueryRow(`
		SELECT state, email_delivery_id, retention_until
		  FROM f08_meta_notification_outbox
		 WHERE id = $1`,
		id,
	).Scan(&state, &storedEmailID, &retentionUntil)
	if err != nil {
		t.Fatal(err)
	}
	sender.mu.Lock()
	if state != "delivered" || storedEmailID != firstEmailID ||
		len(sender.emailIDs) != 1 || sender.calls != 2 ||
		!retentionUntil.Equal(notificationRetentionUntil(now)) {
		t.Fatalf(
			"state=%q email=%q unique=%d calls=%d retention=%s",
			state,
			storedEmailID,
			len(sender.emailIDs),
			sender.calls,
			retentionUntil,
		)
	}
	sender.mu.Unlock()

	permanentID, delivered, err := store.PutIfAbsent(
		context.Background(),
		"instagram_personal",
		workspaceID,
		"post-permanent",
		"channel-permanent",
		permanentKey,
		json.RawMessage(
			`{"format":"image","media":[{"url":"https://media.example/manual.jpg"}]}`,
		),
	)
	if err != nil || delivered {
		t.Fatalf(
			"permanent enqueue id=%q delivered=%v error=%v",
			permanentID,
			delivered,
			err,
		)
	}
	permanentDispatcher, err := NewNotificationDispatcher(
		store,
		notificationSenderFunc(func(
			context.Context,
			NotificationDelivery,
		) (string, error) {
			return "", &publishing.ProviderError{
				Code:   "email_rejected",
				Detail: "permanent downstream rejection",
			}
		}),
		time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	processed, err = permanentDispatcher.DispatchOne(context.Background())
	if err == nil || !processed {
		t.Fatalf("permanent dispatch = %v, %v", processed, err)
	}
	var (
		permanentState     string
		permanentFailedAt  time.Time
		permanentRetention time.Time
	)
	err = database.QueryRow(`
		SELECT state, permanent_failed_at, retention_until
		  FROM f08_meta_notification_outbox
		 WHERE id = $1`,
		permanentID,
	).Scan(&permanentState, &permanentFailedAt, &permanentRetention)
	if err != nil {
		t.Fatal(err)
	}
	if permanentState != "permanent_failure" ||
		!permanentFailedAt.Equal(now) ||
		!permanentRetention.Equal(notificationRetentionUntil(now)) {
		t.Fatalf(
			"permanent state=%q failed=%s retention=%s",
			permanentState,
			permanentFailedAt,
			permanentRetention,
		)
	}
}
