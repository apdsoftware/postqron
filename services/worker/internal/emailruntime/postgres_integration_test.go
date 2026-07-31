package emailruntime

import (
	"context"
	"database/sql"
	"os"
	"sync"
	"testing"
	"time"

	email "github.com/apdsoftware/postqron/features/f14-email"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestSocialDeliveryStateCrashReconciliationFailsWithoutReplay(
	t *testing.T,
) {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DATABASE_URL is not configured")
	}
	database, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	now := time.Now().UTC()
	deliveryID := "email_social_crash_" + now.Format("20060102150405.000000000")
	acceptedID := deliveryID + "_accepted"
	_, err = database.Exec(`
		INSERT INTO f14_email_deliveries (
			id, idempotency_key, channel, template_id, template_version,
			recipient_id, recipient_email, recipient_name, subject, preheader,
			html_body, text_body, locale, state, attempt_count, max_attempts,
			next_attempt_at, lease_token, locked_until,
			provider_call_started_at, source_workspace_id, created_at, updated_at
		) VALUES (
			$1, $2, 'transactional', 'facebook_group_manual_publish', '1.0.0',
			'account-crash', 'crash@example.test', '', 'Manual action',
			'Manual action', '<p>Manual action</p>', 'Manual action', 'en',
			'sending', 1, 5, $3, 'expired-after-call', $3, $3,
			'workspace-crash', $3, $3
		)`,
		deliveryID,
		"social-notification:"+deliveryID,
		now.Add(-3*time.Minute),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = database.Exec(
			`DELETE FROM f14_email_deliveries WHERE id IN ($1, $2)`,
			deliveryID,
			acceptedID,
		)
	})
	service := &Service{database: database}
	id, state, err := service.socialDeliveryState(
		context.Background(),
		deliveryID,
		now,
	)
	if err != nil || id != deliveryID ||
		state != SocialNotificationPermanentFailure {
		t.Fatalf("reconcile = %q, %q, %v", id, state, err)
	}
	var (
		persistedState string
		code           string
		detail         string
	)
	err = database.QueryRow(`
		SELECT state, last_diagnostic_code, last_diagnostic_detail
		  FROM f14_email_deliveries
		 WHERE id = $1`,
		deliveryID,
	).Scan(&persistedState, &code, &detail)
	if err != nil {
		t.Fatal(err)
	}
	if persistedState != "failed" || code != "ambiguous_delivery" ||
		detail != "" {
		t.Fatalf(
			"persisted crash reconciliation = %q, %q, %q",
			persistedState,
			code,
			detail,
		)
	}
	// Reconciliation is deterministic and terminal: another worker observes
	// the same failure and never returns the delivery to F14's send queue.
	_, replayState, err := service.socialDeliveryState(
		context.Background(),
		deliveryID,
		now.Add(time.Minute),
	)
	if err != nil || replayState != SocialNotificationPermanentFailure {
		t.Fatalf("replay reconciliation = %q, %v", replayState, err)
	}

	_, err = database.Exec(`
		INSERT INTO f14_email_deliveries (
			id, idempotency_key, channel, template_id, template_version,
			recipient_id, recipient_email, recipient_name, subject, preheader,
			html_body, text_body, locale, state, attempt_count, max_attempts,
			next_attempt_at, provider_message_id, accepted_at,
			source_workspace_id, created_at, updated_at
		) VALUES (
			$1, $2, 'transactional', 'instagram_personal_manual_publish', '1.0.0',
			'account-accepted', 'accepted@example.test', '', 'Manual action',
			'Manual action', '<p>Manual action</p>', 'Manual action', 'en',
			'accepted', 1, 5, $3, $4, $3, 'workspace-accepted', $3, $3
		)`,
		acceptedID,
		"social-notification:"+acceptedID,
		now,
		"provider-"+acceptedID,
	)
	if err != nil {
		t.Fatal(err)
	}
	id, acceptedState, err := service.socialDeliveryState(
		context.Background(),
		acceptedID,
		now,
	)
	if err != nil || id != acceptedID ||
		acceptedState != SocialNotificationPending {
		t.Fatalf("accepted receipt = %q, %q, %v", id, acceptedState, err)
	}
	var (
		persistedAcceptedState string
		persistedProviderID    string
	)
	err = database.QueryRow(`
		SELECT state, provider_message_id
		  FROM f14_email_deliveries
		 WHERE id = $1`,
		acceptedID,
	).Scan(&persistedAcceptedState, &persistedProviderID)
	if err != nil {
		t.Fatal(err)
	}
	if persistedAcceptedState != "accepted" ||
		persistedProviderID != "provider-"+acceptedID {
		t.Fatalf(
			"queued receipt was mutated = %q, %q",
			persistedAcceptedState,
			persistedProviderID,
		)
	}
}

func TestSocialEmailWorkspaceBindingIsAtomicAndImmutable(t *testing.T) {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DATABASE_URL is not configured")
	}
	database, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	now := time.Now().UTC()
	suffix := now.Format("20060102150405.000000000")
	deliveryID := "email_atomic_" + suffix
	orphanID := "email_orphan_" + suffix
	key := "social-notification:atomic-" + suffix
	t.Cleanup(func() {
		_, _ = database.Exec(
			`DELETE FROM f14_email_deliveries WHERE id IN ($1, $2)`,
			deliveryID,
			orphanID,
		)
	})
	delivery := email.Delivery{
		Message: email.Message{
			ID:                deliveryID,
			IdempotencyKey:    key,
			Channel:           email.ChannelTransactional,
			Template:          email.TemplateFacebookGroupManual,
			TemplateVersion:   "1.0.0",
			SourceWorkspaceID: "workspace-atomic",
			CreatedAt:         now,
			MaxAttempts:       5,
		},
		Rendered: email.RenderedMessage{
			Recipient: email.Recipient{
				ID:    "account-atomic",
				Email: "atomic-pii@example.test",
				Name:  "Atomic PII",
			},
			Locale:    email.LocaleEnglish,
			Subject:   "Atomic PII",
			Preheader: "Atomic PII",
			HTML:      "<p>Atomic PII</p>",
			Text:      "Atomic PII",
		},
		State:         email.StatePending,
		NextAttemptAt: now,
	}
	store := &sqlStore{database: database}
	result, err := store.Enqueue(context.Background(), delivery)
	if err != nil || !result.Created {
		t.Fatalf("atomic enqueue = %+v, %v", result, err)
	}
	var workspaceID string
	err = database.QueryRow(`
		SELECT source_workspace_id
		  FROM f14_email_deliveries
		 WHERE id = $1
		   AND recipient_email = 'atomic-pii@example.test'
		   AND recipient_name = 'Atomic PII'
		   AND html_body LIKE '%Atomic PII%'
		   AND text_body LIKE '%Atomic PII%'`,
		deliveryID,
	).Scan(&workspaceID)
	if err != nil || workspaceID != "workspace-atomic" {
		t.Fatalf("persisted social workspace = %q, %v", workspaceID, err)
	}
	confusedDeputy := delivery
	confusedDeputy.Message.ID = "email_conflict_" + suffix
	confusedDeputy.Message.SourceWorkspaceID = "workspace-other"
	if _, err = store.Enqueue(
		context.Background(),
		confusedDeputy,
	); err == nil {
		t.Fatal("idempotency replay changed social workspace binding")
	}
	_, err = database.Exec(`
		INSERT INTO f14_email_deliveries (
			id, idempotency_key, channel, template_id, template_version,
			recipient_id, recipient_email, recipient_name, subject, preheader,
			html_body, text_body, locale, state, attempt_count, max_attempts,
			next_attempt_at, created_at, updated_at
		) VALUES (
			$1, $2, 'transactional', 'facebook_group_manual_publish', '1.0.0',
			'account-orphan', 'orphan-pii@example.test', 'Orphan PII',
			'Orphan PII', 'Orphan PII', '<p>Orphan PII</p>', 'Orphan PII',
			'en', 'pending', 0, 5, $3, $3, $3
		)`,
		orphanID,
		"social-notification:orphan-"+suffix,
		now,
	)
	if err == nil {
		t.Fatal("schema accepted social delivery PII without workspace binding")
	}
	var orphanCount int
	if queryErr := database.QueryRow(`
		SELECT count(*)
		  FROM f14_email_deliveries
		 WHERE id = $1 OR (
		       template_id IN (
		           'facebook_group_manual_publish',
		           'instagram_personal_manual_publish'
		       )
		       AND source_workspace_id IS NULL
		       AND recipient_email = 'orphan-pii@example.test'
		 )`,
		orphanID,
	).Scan(&orphanCount); queryErr != nil || orphanCount != 0 {
		t.Fatalf("orphan social PII rows = %d, %v", orphanCount, queryErr)
	}
}

func TestEmailLeaseReclaimsOnlyBeforeProviderCall(t *testing.T) {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DATABASE_URL is not configured")
	}
	database, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	now := time.Now().UTC()
	suffix := now.Format("20060102150405.000000000")
	preCallID := "email_pre_call_" + suffix
	afterCallID := "email_after_call_" + suffix
	for index, id := range []string{preCallID, afterCallID} {
		dueAt := now
		if index == 1 {
			dueAt = now.Add(emailDeliveryLease + time.Second)
		}
		_, err = database.Exec(`
			INSERT INTO f14_email_deliveries (
				id, idempotency_key, channel, template_id, template_version,
				recipient_id, recipient_email, recipient_name, subject, preheader,
				html_body, text_body, locale, state, attempt_count, max_attempts,
				next_attempt_at, source_workspace_id, created_at, updated_at
			) VALUES (
				$1, $2, 'transactional', 'facebook_group_manual_publish', '1.0.0',
				'account-lease', 'lease@example.test', 'Lease PII',
				'Manual action', 'Manual action', '<p>PII</p>', 'PII', 'en',
				'pending', 0, 5, $3, 'workspace-lease', $3, $3
			)`,
			id,
			"social-notification:"+id,
			dueAt,
		)
		if err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		_, _ = database.Exec(
			`DELETE FROM f14_email_deliveries WHERE id IN ($1, $2)`,
			preCallID,
			afterCallID,
		)
	})
	store := &sqlStore{database: database}
	first, found, err := store.ClaimDue(context.Background(), now)
	if err != nil || !found || first.Message.ID != preCallID ||
		first.LeaseToken == "" {
		t.Fatalf("first claim = %+v, %v, %v", first, found, err)
	}
	reclaimed, found, err := store.ClaimDue(
		context.Background(),
		now.Add(emailDeliveryLease+time.Second),
	)
	if err != nil || !found || reclaimed.Message.ID != preCallID ||
		reclaimed.LeaseToken == first.LeaseToken || reclaimed.Attempt != 2 {
		t.Fatalf("pre-call reclaim = %+v, %v, %v", reclaimed, found, err)
	}
	if err = store.MarkProviderCallStarted(
		context.Background(),
		preCallID,
		first.LeaseToken,
		now.Add(emailDeliveryLease+time.Second),
	); err == nil {
		t.Fatal("expired lease token unexpectedly won CAS")
	}
	if err = store.MarkProviderCallStarted(
		context.Background(),
		preCallID,
		reclaimed.LeaseToken,
		now.Add(emailDeliveryLease+time.Second),
	); err != nil {
		t.Fatal(err)
	}

	second, found, err := store.ClaimDue(
		context.Background(),
		now.Add(emailDeliveryLease+time.Second),
	)
	if err != nil || !found || second.Message.ID != afterCallID {
		t.Fatalf("second claim = %+v, %v, %v", second, found, err)
	}
	callAt := now.Add(emailDeliveryLease + time.Second)
	if err = store.MarkProviderCallStarted(
		context.Background(),
		afterCallID,
		second.LeaseToken,
		callAt,
	); err != nil {
		t.Fatal(err)
	}
	_, found, err = store.ClaimDue(
		context.Background(),
		callAt.Add(emailDeliveryLease+time.Second),
	)
	if err != nil || found {
		t.Fatalf("post-call delivery was replayable: found=%v error=%v", found, err)
	}
	service := &Service{database: database}
	_, state, err := service.socialDeliveryState(
		context.Background(),
		afterCallID,
		callAt.Add(emailDeliveryLease+time.Second),
	)
	if err != nil || state != SocialNotificationPermanentFailure {
		t.Fatalf("post-call reconciliation = %q, %v", state, err)
	}
}

func TestEmailLeaseConcurrentClaimAndExpiredPIIPurge(t *testing.T) {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DATABASE_URL is not configured")
	}
	database, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	now := time.Now().UTC()
	suffix := now.Format("20060102150405.000000000")
	claimID := "email_race_" + suffix
	expiredID := "email_expired_" + suffix
	providerID := "provider_" + suffix
	_, err = database.Exec(`
		INSERT INTO f14_email_deliveries (
			id, idempotency_key, channel, template_id, template_version,
			recipient_id, recipient_email, recipient_name, subject, preheader,
			html_body, text_body, locale, state, attempt_count, max_attempts,
			next_attempt_at, source_workspace_id, created_at, updated_at
		) VALUES (
			$1, $2, 'transactional', 'facebook_group_manual_publish', '1.0.0',
			'account-race', 'race@example.test', 'Race PII', 'PII subject',
			'PII preheader', '<p>PII body</p>', 'PII body', 'en',
			'pending', 0, 5, $3, 'workspace-race', $3, $3
		)`,
		claimID,
		"social-notification:"+claimID,
		now,
	)
	if err == nil {
		_, err = database.Exec(`
		INSERT INTO f14_email_deliveries (
			id, idempotency_key, channel, template_id, template_version,
			recipient_id, recipient_email, recipient_name, subject, preheader,
			html_body, text_body, locale, state, attempt_count, max_attempts,
			next_attempt_at, provider_message_id, accepted_at,
			source_workspace_id, retention_until, created_at, updated_at
		) VALUES (
			$1, $2, 'transactional', 'instagram_personal_manual_publish', '1.0.0',
			'account-expired', 'expired@example.test', 'Expired PII',
			'PII subject', 'PII preheader', '<p>PII body</p>', 'PII body', 'en',
			'accepted', 1, 5, $3, $4, $3, 'workspace-expired', $5, $3, $3
		)`,
			expiredID,
			"social-notification:"+expiredID,
			now,
			providerID,
			now.Add(-time.Second),
		)
	}
	if err == nil {
		_, err = database.Exec(`
		INSERT INTO f14_email_provider_events (
			provider_event_id, provider_message_id, event_type, recipient_id,
			diagnostic_code, diagnostic_detail, occurred_at
		) VALUES ($1, $2, 'delivered', 'account-expired', '', 'PII detail', $3)`,
			"event_"+suffix,
			providerID,
			now,
		)
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = database.Exec(
			`DELETE FROM f14_email_provider_events WHERE provider_message_id = $1`,
			providerID,
		)
		_, _ = database.Exec(
			`DELETE FROM f14_email_deliveries WHERE id IN ($1, $2)`,
			claimID,
			expiredID,
		)
	})
	store := &sqlStore{database: database}
	var wait sync.WaitGroup
	results := make(chan bool, 16)
	for range 16 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			delivery, found, claimErr := store.ClaimDue(
				context.Background(),
				now,
			)
			results <- claimErr == nil && found && delivery.Message.ID == claimID
		}()
	}
	wait.Wait()
	close(results)
	winners := 0
	for won := range results {
		if won {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("concurrent lease winners = %d, want 1", winners)
	}
	service := &Service{database: database}
	purged, err := service.purgeExpiredDeliveries(context.Background(), now)
	if err != nil || purged != 1 {
		t.Fatalf("purge = %d, %v", purged, err)
	}
	var residual int
	err = database.QueryRow(`
		SELECT
		    (SELECT count(*) FROM f14_email_deliveries WHERE id = $1)
		    + (SELECT count(*) FROM f14_email_provider_events
		        WHERE provider_message_id = $2)`,
		expiredID,
		providerID,
	).Scan(&residual)
	if err != nil || residual != 0 {
		t.Fatalf("expired F14 PII residual rows = %d, %v", residual, err)
	}
}
