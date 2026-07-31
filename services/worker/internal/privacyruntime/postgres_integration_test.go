package privacyruntime

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestEmailPIIErasureRemovesAccountAndWorkspaceDeliveries(t *testing.T) {
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
	accountID := "account-email-erase-" + suffix
	workspaceID := "workspace-email-erase-" + suffix
	accountDeliveryID := "email-account-erase-" + suffix
	workspaceDeliveryID := "email-workspace-erase-" + suffix
	accountProviderID := "provider-account-erase-" + suffix
	workspaceProviderID := "provider-workspace-erase-" + suffix
	_, err = database.Exec(`
		INSERT INTO f14_email_deliveries (
			id, idempotency_key, channel, template_id, template_version,
			recipient_id, recipient_email, recipient_name, subject, preheader,
			html_body, text_body, locale, state, attempt_count, max_attempts,
			next_attempt_at, provider_message_id, accepted_at,
			source_workspace_id, retention_until, created_at, updated_at
		) VALUES
		(
			$1, $2, 'transactional', 'facebook_group_manual_publish', '1.0.0',
			$3, 'account-pii@example.test', 'Account PII', 'Account PII',
			'Account PII', '<p>Account PII</p>', 'Account PII', 'en',
			'accepted', 1, 5, $4, $5, $4, 'workspace-account-erase',
			$6, $4, $4
		),
		(
			$7, $8, 'transactional', 'instagram_personal_manual_publish', '1.0.0',
			'other-account', 'workspace-pii@example.test', 'Workspace PII',
			'Workspace PII', 'Workspace PII', '<p>Workspace PII</p>',
			'Workspace PII', 'en', 'accepted', 1, 5, $4, $9, $4, $10,
			$6, $4, $4
		)`,
		accountDeliveryID,
		"social-notification:"+accountDeliveryID,
		accountID,
		now,
		accountProviderID,
		now.AddDate(1, 0, 0),
		workspaceDeliveryID,
		"social-notification:"+workspaceDeliveryID,
		workspaceProviderID,
		workspaceID,
	)
	if err == nil {
		_, err = database.Exec(`
		INSERT INTO f14_email_provider_events (
			provider_event_id, provider_message_id, event_type, recipient_id,
			diagnostic_code, diagnostic_detail, occurred_at
		) VALUES
			($1, $2, 'delivered', $3, '', 'Account PII', $4),
			($5, $6, 'delivered', 'other-account', '', 'Workspace PII', $4)`,
			"event-account-erase-"+suffix,
			accountProviderID,
			accountID,
			now,
			"event-workspace-erase-"+suffix,
			workspaceProviderID,
		)
	}
	if err == nil {
		_, err = database.Exec(`
		INSERT INTO f14_email_suppressions (
			recipient_id, scope, reason, occurred_at
		) VALUES ($1, 'all', 'Account PII', $2)`,
			accountID,
			now,
		)
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = database.Exec(
			`DELETE FROM f14_email_provider_events
			  WHERE provider_message_id IN ($1, $2)`,
			accountProviderID,
			workspaceProviderID,
		)
		_, _ = database.Exec(
			`DELETE FROM f14_email_deliveries WHERE id IN ($1, $2)`,
			accountDeliveryID,
			workspaceDeliveryID,
		)
		_, _ = database.Exec(
			`DELETE FROM f14_email_suppressions WHERE recipient_id = $1`,
			accountID,
		)
	})

	transaction, err := database.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = eraseAccountEmailData(
		context.Background(),
		transaction,
		accountID,
	); err == nil {
		err = eraseWorkspaceEmailData(
			context.Background(),
			transaction,
			workspaceID,
		)
	}
	if err != nil {
		_ = transaction.Rollback()
		t.Fatal(err)
	}
	if err = transaction.Commit(); err != nil {
		t.Fatal(err)
	}

	var residual int
	err = database.QueryRow(`
		SELECT
		    (SELECT count(*) FROM f14_email_deliveries
		      WHERE id IN ($1, $2)
		        AND (
		            recipient_email IN (
		             'account-pii@example.test',
		             'workspace-pii@example.test'
		            )
		            OR recipient_name IN ('Account PII', 'Workspace PII')
		            OR html_body LIKE '%PII%'
		            OR text_body LIKE '%PII%'
		        ))
		    + (SELECT count(*) FROM f14_email_provider_events
		        WHERE provider_message_id IN ($3, $4)
		          AND diagnostic_detail LIKE '%PII%')
		    + (SELECT count(*) FROM f14_email_suppressions
		        WHERE recipient_id = $5 AND reason LIKE '%PII%')`,
		accountDeliveryID,
		workspaceDeliveryID,
		accountProviderID,
		workspaceProviderID,
		accountID,
	).Scan(&residual)
	if err != nil || residual != 0 {
		t.Fatalf("F14 PII residual rows = %d, %v", residual, err)
	}
}
