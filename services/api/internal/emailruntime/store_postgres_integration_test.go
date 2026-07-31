package emailruntime

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestPostgresStoreReconcilesExpiredNonSocialPostCallLease(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		databaseURL = os.Getenv("DATABASE_URL")
	}
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL or DATABASE_URL is not configured")
	}
	database, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	now := time.Now().UTC()
	id := "email_api_ambiguous_" +
		now.Format("20060102150405.000000000")
	_, err = database.Exec(`
		INSERT INTO f14_email_deliveries (
			id, idempotency_key, channel, template_id, template_version,
			recipient_id, recipient_email, recipient_name, subject, preheader,
			html_body, text_body, locale, state, attempt_count, max_attempts,
			next_attempt_at, lease_token, locked_until,
			provider_call_started_at, created_at, updated_at
		) VALUES (
			$1, $2, 'transactional', 'account_security', '1.0.0',
			'account-api-ambiguous', 'api-ambiguous-pii@example.test',
			'API Ambiguous PII', 'API Ambiguous PII', 'API Ambiguous PII',
			'<p>API Ambiguous PII</p>', 'API Ambiguous PII', 'en',
			'sending', 1, 5, $3, 'api-expired-lease', $4, $5, $3, $3
		)`,
		id,
		"api-ambiguous:"+id,
		now.Add(-2*time.Minute),
		now.Add(-time.Minute),
		now.Add(-90*time.Second),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = database.Exec(
			`DELETE FROM f14_email_deliveries WHERE id = $1`,
			id,
		)
	})
	reconciled, err := (&sqlStore{database: database}).
		ReconcileExpiredLeases(context.Background(), now)
	if err != nil || reconciled != 1 {
		t.Fatalf("ReconcileExpiredLeases() = %d, %v", reconciled, err)
	}
	var (
		state       string
		code        string
		retention   time.Time
		leaseToken  sql.NullString
		lockedUntil sql.NullTime
		callStarted sql.NullTime
	)
	err = database.QueryRow(`
		SELECT state, last_diagnostic_code, retention_until,
		       lease_token, locked_until, provider_call_started_at
		  FROM f14_email_deliveries
		 WHERE id = $1`,
		id,
	).Scan(
		&state,
		&code,
		&retention,
		&leaseToken,
		&lockedUntil,
		&callStarted,
	)
	if err != nil || state != "failed" || code != "ambiguous_delivery" ||
		!retention.After(now) || leaseToken.Valid || lockedUntil.Valid ||
		callStarted.Valid {
		t.Fatalf(
			"reconciled state=%q code=%q retention=%s lease=%v lock=%v call=%v error=%v",
			state,
			code,
			retention,
			leaseToken,
			lockedUntil,
			callStarted,
			err,
		)
	}
}
