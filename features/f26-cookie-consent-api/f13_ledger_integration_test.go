package cookieconsent

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// Digest of the F25-approved cookies_it artifact
// (features/f25-legal-documents/src/bundle.ts, ref.
// LEGAL-APPROVAL-2026-07-25-F25). Cross-checked against the checked-in F13
// seed migration by
// features/f25-legal-documents/test/cookie-consent-seed.test.ts, so this
// constant cannot silently drift from the canonical corpus without both
// suites failing.
const approvedCookiesItDigest = "9f6e657791afa9c0f54a448ffcdaf031cc244e218f2bbea27ed1d1563cf8bd33"

const cookiesItSeedMigrationPath = "../f13-compliance/migrations/000002_seed_cookies_it_release.sql"

// Regression coverage for the production defect (issue #150): the F26
// cookie-preferences API returned 503 cookie_policy_unavailable because no
// approved cookies_it row existed in compliance_legal_documents, even though
// the F25 corpus was approved and bundled.

func TestPostgresPolicySourceResolvesTheApprovedCookiesItRelease(t *testing.T) {
	database := openLedgerIntegrationDatabase(t)
	policies, err := NewPostgresPolicySource(database)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := policies.Current(context.Background(), time.Now().UTC())
	if err != nil {
		t.Fatalf("current cookie policy: %v", err)
	}
	if policy.Version != "0.1" {
		t.Fatalf("version = %q, want 0.1", policy.Version)
	}
	if policy.DigestSHA256 != approvedCookiesItDigest {
		t.Fatalf(
			"digest = %q, want %q (the F25-approved bundle digest)",
			policy.DigestSHA256,
			approvedCookiesItDigest,
		)
	}
	if policy.EffectiveAt.IsZero() || policy.EffectiveAt.After(time.Now().UTC()) {
		t.Fatalf("effectiveAt = %s is not an already-effective instant", policy.EffectiveAt)
	}
}

func TestCookiesItSeedMigrationIsIdempotentWhenReapplied(t *testing.T) {
	database := openLedgerIntegrationDatabase(t)
	seed, err := os.ReadFile(cookiesItSeedMigrationPath)
	if err != nil {
		t.Fatal(err)
	}
	for pass := range 2 {
		if _, err := database.Exec(string(seed)); err != nil {
			t.Fatalf("reapply seed migration (pass %d): %v", pass, err)
		}
	}
	var rowCount int
	if err := database.QueryRow(`
		SELECT count(*) FROM compliance_legal_documents
		WHERE document_key = 'cookies_it' AND version = '0.1'`,
	).Scan(&rowCount); err != nil {
		t.Fatal(err)
	}
	if rowCount != 1 {
		t.Fatalf(
			"compliance_legal_documents has %d cookies_it/0.1 rows after reapplying the seed twice, want exactly 1",
			rowCount,
		)
	}
}

type fixedSubjectResolver struct{ subject Subject }

func (resolver fixedSubjectResolver) Resolve(
	context.Context,
	*http.Request,
) (Resolution, error) {
	return Resolution{Subject: resolver.subject}, nil
}

func TestAnonymousVisitorReadsAndSavesPreferencesAgainstTheRealLedger(t *testing.T) {
	database := openLedgerIntegrationDatabase(t)
	repository, err := NewPostgresRepository(database)
	if err != nil {
		t.Fatal(err)
	}
	policies, err := NewPostgresPolicySource(database)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	service, err := NewService(repository, policies, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	suffix := fmt.Sprint(time.Now().UnixNano())
	resolver := fixedSubjectResolver{
		subject: Subject{Kind: SubjectBrowser, ID: "ledger-integration-browser-" + suffix},
	}
	handler, err := NewHTTPHandler(service, resolver)
	if err != nil {
		t.Fatal(err)
	}

	first := httptest.NewRecorder()
	handler.ServeHTTP(first, httptest.NewRequest(
		http.MethodGet, "/api/v1/cookie-preferences", nil,
	))
	if first.Code != http.StatusOK {
		t.Fatalf("first visit status = %d body=%s", first.Code, first.Body.String())
	}
	var initial PreferenceState
	if err := json.Unmarshal(first.Body.Bytes(), &initial); err != nil {
		t.Fatal(err)
	}
	if !initial.Necessary || initial.HasRecordedChoice ||
		initial.Analytics || initial.Marketing || initial.Preferences {
		t.Fatalf("initial state = %+v, want necessary-only default-deny", initial)
	}
	if initial.PolicyVersion != "0.1" {
		t.Fatalf("policy version = %q, want the real ledger version 0.1", initial.PolicyVersion)
	}

	put := httptest.NewRequest(
		http.MethodPut,
		"/api/v1/cookie-preferences",
		strings.NewReader(
			`{"policy_version":"0.1","source":"banner","preferences":false,"analytics":true,"marketing":false}`,
		),
	)
	put.Header.Set("Content-Type", "application/json")
	put.Header.Set("Idempotency-Key", "ledger-integration-put-"+suffix)
	putResponse := httptest.NewRecorder()
	handler.ServeHTTP(putResponse, put)
	if putResponse.Code != http.StatusOK {
		t.Fatalf("put status = %d body=%s", putResponse.Code, putResponse.Body.String())
	}
	var saved PreferenceState
	if err := json.Unmarshal(putResponse.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	if !saved.Analytics || saved.Marketing || saved.Preferences || !saved.HasRecordedChoice {
		t.Fatalf("saved state = %+v", saved)
	}

	second := httptest.NewRecorder()
	handler.ServeHTTP(second, httptest.NewRequest(
		http.MethodGet, "/api/v1/cookie-preferences", nil,
	))
	var reread PreferenceState
	if err := json.Unmarshal(second.Body.Bytes(), &reread); err != nil {
		t.Fatal(err)
	}
	if !reread.Analytics || reread.Marketing || reread.Preferences {
		t.Fatalf("reread state after PUT = %+v", reread)
	}
}

func TestNewCookiesItPolicyVersionInvalidatesPriorConsentOnTheRealLedger(t *testing.T) {
	database := openLedgerIntegrationDatabase(t)
	repository, err := NewPostgresRepository(database)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	suffix := fmt.Sprint(time.Now().UnixNano())
	subject := Subject{Kind: SubjectBrowser, ID: "ledger-version-bump-" + suffix}
	service, err := NewService(repository, &StaticPolicySource{Policy: PolicyRelease{
		Version:      "0.1",
		DigestSHA256: approvedCookiesItDigest,
		EffectiveAt:  now.Add(-time.Hour),
	}}, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.Put(
		context.Background(), subject, "0.1", Selection{Analytics: true}, "banner",
		"ledger-version-bump-put-"+suffix,
	); err != nil {
		t.Fatal(err)
	}

	// A new cookies_it release supersedes the current one, exactly as a
	// future legal-review update would after this hotfix.
	bumped, err := NewService(repository, &StaticPolicySource{Policy: PolicyRelease{
		Version:      "0.2",
		DigestSHA256: "b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2",
		EffectiveAt:  now.Add(-time.Minute),
	}}, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	state, err := bumped.Get(context.Background(), subject)
	if err != nil {
		t.Fatal(err)
	}
	if state.HasRecordedChoice || state.Analytics {
		t.Fatalf("state after policy version bump = %+v, want the prior choice invalidated", state)
	}
}

// TestSeedMigrationFailsClosedOnADivergentPreexistingRow proves that
// ON CONFLICT DO NOTHING never masks a pre-existing cookies_it/0.1 row that
// diverges from the F25-approved bundle: the migration must error instead of
// silently leaving stale/hand-edited/corrupted data in place.
func TestSeedMigrationFailsClosedOnADivergentPreexistingRow(t *testing.T) {
	pool := cookiesLedgerSchemaPool(t)
	ctx := context.Background()

	const divergentDigest = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	if _, err := pool.Exec(ctx, `
		INSERT INTO compliance_legal_documents (
			document_key, jurisdiction, locale, version, content_bytes,
			digest_sha256, content_status, legal_approval_id, approved_at,
			published_at, effective_at, permanent_url, current_url, change_type
		) VALUES (
			'cookies_it', 'IT', 'it-IT', '0.1',
			convert_to('a hand-edited or stale cookies_it document', 'UTF8'),
			$1, 'approved', 'DIVERGENT-TEST-APPROVAL', now(), now(), now(),
			'/legal/divergent', '/legal/divergent/current', 'non_material'
		)`,
		divergentDigest,
	); err != nil {
		t.Fatalf("seed a divergent pre-existing row: %v", err)
	}

	seed, err := os.ReadFile(cookiesItSeedMigrationPath)
	if err != nil {
		t.Fatal(err)
	}
	transaction, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, execErr := transaction.Exec(ctx, string(seed))
	if execErr == nil {
		_ = transaction.Rollback(ctx)
		t.Fatal("expected the seed migration to fail closed against a divergent pre-existing row, got nil error")
	}
	if err := transaction.Rollback(ctx); err != nil {
		t.Fatal(err)
	}

	var rowCount int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM compliance_legal_documents
		WHERE document_key = 'cookies_it' AND version = '0.1'`,
	).Scan(&rowCount); err != nil {
		t.Fatal(err)
	}
	if rowCount != 1 {
		t.Fatalf(
			"after the failed migration attempt: %d cookies_it/0.1 rows remain, want exactly 1 (the untouched divergent row)",
			rowCount,
		)
	}
	var storedDigest string
	if err := pool.QueryRow(ctx, `
		SELECT digest_sha256 FROM compliance_legal_documents
		WHERE document_key = 'cookies_it' AND version = '0.1'`,
	).Scan(&storedDigest); err != nil {
		t.Fatal(err)
	}
	if storedDigest != divergentDigest {
		t.Fatalf(
			"stored digest = %q, want the untouched divergent digest %q (the migration must not have overwritten it)",
			storedDigest, divergentDigest,
		)
	}
}

func cookiesLedgerSchemaPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	databaseURL := integrationDatabaseURL()
	if databaseURL == "" {
		t.Skip("set F26_DATABASE_URL or DATABASE_URL after applying the F13 and F26 migrations")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}

	schema := fmt.Sprintf("f26_ledger_reject_test_%d", time.Now().UnixNano())
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		admin.Close()
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		admin.Close()
		t.Fatal(err)
	}

	ledgerMigration, err := os.ReadFile(filepath.Join(
		"..", "f13-compliance", "migrations", "000001_create_compliance_ledger.sql",
	))
	if err != nil {
		pool.Close()
		admin.Close()
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(ledgerMigration)); err != nil {
		pool.Close()
		admin.Close()
		t.Fatal(err)
	}

	t.Cleanup(func() {
		pool.Close()
		_, _ = admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		admin.Close()
	})
	return pool
}

func openLedgerIntegrationDatabase(t *testing.T) *sql.DB {
	t.Helper()
	databaseURL := integrationDatabaseURL()
	if databaseURL == "" {
		t.Skip("set F26_DATABASE_URL or DATABASE_URL after applying the F13 and F26 migrations")
	}
	if _, err := os.Stat(filepath.FromSlash(cookiesItSeedMigrationPath)); err != nil {
		t.Fatalf("seed migration fixture missing: %v", err)
	}
	database, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := database.Ping(); err != nil {
		t.Fatal(err)
	}
	return database
}
