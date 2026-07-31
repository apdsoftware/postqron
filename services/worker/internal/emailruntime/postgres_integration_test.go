package emailruntime

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

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
			next_attempt_at, created_at, updated_at
		) VALUES (
			$1, $2, 'transactional', 'facebook_group_manual_publish', '1.0.0',
			'account-crash', 'crash@example.test', '', 'Manual action',
			'Manual action', '<p>Manual action</p>', 'Manual action', 'en',
			'sending', 1, 5, $3, $3, $3
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
			next_attempt_at, provider_message_id, accepted_at, created_at, updated_at
		) VALUES (
			$1, $2, 'transactional', 'instagram_personal_manual_publish', '1.0.0',
			'account-accepted', 'accepted@example.test', '', 'Manual action',
			'Manual action', '<p>Manual action</p>', 'Manual action', 'en',
			'accepted', 1, 5, $3, $4, $3, $3, $3
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
		acceptedState != SocialNotificationDelivered {
		t.Fatalf("accepted receipt = %q, %q, %v", id, acceptedState, err)
	}
}
