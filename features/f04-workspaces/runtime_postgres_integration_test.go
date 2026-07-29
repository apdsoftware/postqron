package workspaces

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestPostgresRuntimeOnboardingIntegration(t *testing.T) {
	databaseURL := os.Getenv("F04_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("F04_DATABASE_URL is not set")
	}
	database, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err = database.PingContext(context.Background()); err != nil {
		t.Fatal(err)
	}

	repository, err := NewPostgresRepository(database)
	if err != nil {
		t.Fatal(err)
	}
	runtimeService, err := NewRuntimeServiceWithClock(
		repository,
		func() time.Time { return testNow },
	)
	if err != nil {
		t.Fatal(err)
	}
	domainService, err := NewService(
		repository,
		fixedLimits{limit: 5, available: true},
		testEmailKey,
		WithClock(func() time.Time { return testNow }),
	)
	if err != nil {
		t.Fatal(err)
	}

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	version := fmt.Sprintf("%d.%d", time.Now().Unix(), time.Now().Nanosecond())
	account := AppSessionAccount{
		ID:              "runtime-account-" + suffix,
		DisplayName:     "Runtime User",
		Email:           "runtime-" + suffix + "@example.com",
		Locale:          LocaleIT,
		ContractCountry: "IT",
	}
	if err = seedRuntimeAuthAccount(database, account, testNow); err != nil {
		t.Fatal(err)
	}
	if err = seedRuntimeLegalDocuments(database, version, testNow); err != nil {
		t.Fatal(err)
	}

	command := CompleteOnboardingCommand{
		Account: account,
		Consents: []OnboardingConsentReceipt{
			{
				DocumentKey:   "terms",
				Version:       version,
				DigestSHA256:  strings.Repeat("a", 64),
				Action:        "accepted",
				Locale:        LocaleIT,
				Purpose:       "contract",
				Surface:       "app_onboarding",
				ControlTextID: "app.consent.terms.v1",
			},
			{
				DocumentKey:   "privacy",
				Version:       version,
				DigestSHA256:  strings.Repeat("b", 64),
				Action:        "accepted",
				Locale:        LocaleIT,
				Purpose:       "privacy_acknowledgement",
				Surface:       "app_onboarding",
				ControlTextID: "app.consent.privacy.v1",
			},
		},
		Workspace: OnboardingWorkspaceInput{
			Mode: "create",
			Name: "Runtime Workspace",
		},
	}

	const attempts = 8
	results := make(chan AppSession, attempts)
	failures := make(chan error, attempts)
	var wait sync.WaitGroup
	for range attempts {
		wait.Add(1)
		go func() {
			defer wait.Done()
			session, _, err := runtimeService.CompleteOnboarding(context.Background(), command)
			if err != nil {
				failures <- err
				return
			}
			results <- session
		}()
	}
	wait.Wait()
	close(results)
	close(failures)
	for err := range failures {
		t.Fatalf("CompleteOnboarding() error = %v", err)
	}

	var workspaceID string
	for session := range results {
		if session.CurrentWorkspace == nil {
			t.Fatal("current workspace is nil")
		}
		if workspaceID == "" {
			workspaceID = session.CurrentWorkspace.ID
		}
		if session.CurrentWorkspace.ID != workspaceID {
			t.Fatalf("workspace id = %q, want %q", session.CurrentWorkspace.ID, workspaceID)
		}
	}

	var workspaceCount, selectionCount, consentCount int
	if err = database.QueryRowContext(
		context.Background(),
		`SELECT count(*) FROM f04_workspaces WHERE personal_account_id = $1`,
		account.ID,
	).Scan(&workspaceCount); err != nil {
		t.Fatal(err)
	}
	if workspaceCount != 1 {
		t.Fatalf("workspace count = %d, want 1", workspaceCount)
	}
	if err = database.QueryRowContext(
		context.Background(),
		`SELECT count(*) FROM f04_workspace_selections WHERE account_id = $1 AND workspace_id = $2`,
		account.ID,
		workspaceID,
	).Scan(&selectionCount); err != nil {
		t.Fatal(err)
	}
	if selectionCount != 1 {
		t.Fatalf("selection count = %d, want 1", selectionCount)
	}
	if err = database.QueryRowContext(
		context.Background(),
		`SELECT count(*)
		 FROM compliance_consent_events
		 WHERE subject_id = $1
		   AND workspace_id = $2
		   AND document_key IN ('terms_it', 'privacy_it')`,
		account.ID,
		workspaceID,
	).Scan(&consentCount); err != nil {
		t.Fatal(err)
	}
	if consentCount != 2 {
		t.Fatalf("consent count = %d, want 2", consentCount)
	}

	otherOwner := "runtime-owner-" + suffix
	otherWorkspace, _, err := domainService.EnsurePersonalWorkspace(
		context.Background(),
		otherOwner,
		"Secondary workspace",
	)
	if err != nil {
		t.Fatal(err)
	}
	invitation, err := domainService.Invite(
		context.Background(),
		otherWorkspace.ID,
		otherOwner,
		account.Email,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = domainService.AcceptInvitation(
		context.Background(),
		invitation.Token,
		account.ID,
		account.Email,
		true,
	); err != nil {
		t.Fatal(err)
	}
	if err = runtimeService.SelectWorkspace(
		context.Background(),
		account,
		otherWorkspace.ID,
	); err != nil {
		t.Fatal(err)
	}
	current, role, err := runtimeService.CurrentWorkspace(context.Background(), account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.ID != otherWorkspace.ID || role != RoleMember {
		t.Fatalf("current workspace = %#v/%s", current, role)
	}
	if err = runtimeService.SelectWorkspace(
		context.Background(),
		account,
		"workspace-denied",
	); !errors.Is(err, ErrForbidden) {
		t.Fatalf("unauthorized SelectWorkspace() error = %v, want forbidden", err)
	}
}

func seedRuntimeAuthAccount(
	database *sql.DB,
	account AppSessionAccount,
	now time.Time,
) error {
	_, err := database.Exec(
		`INSERT INTO auth_accounts (
			id, email, normalized_email, display_name, contract_country, created_at, email_verified_at
		 ) VALUES ($1, $2, lower($2), $3, $4, $5, $5)`,
		account.ID,
		account.Email,
		account.DisplayName,
		account.ContractCountry,
		now,
	)
	return err
}

func seedRuntimeLegalDocuments(
	database *sql.DB,
	version string,
	now time.Time,
) error {
	for _, document := range []struct {
		key    string
		digest string
	}{
		{key: "terms_it", digest: strings.Repeat("a", 64)},
		{key: "privacy_it", digest: strings.Repeat("b", 64)},
	} {
		if _, err := database.Exec(
			`INSERT INTO compliance_legal_documents (
				document_key, jurisdiction, locale, version, content_bytes,
				digest_sha256, content_status, legal_approval_id, approved_at,
				published_at, effective_at, permanent_url, current_url, change_type
			 ) VALUES (
				$1, 'IT', 'it-IT', $2, $3, $4, 'approved', $5, $6, $6, $6, $7, $8, 'material'
			 )`,
			document.key,
			version,
			[]byte(document.key+"-"+version),
			document.digest,
			"approval-"+document.key+"-"+version,
			now.Add(-time.Hour),
			"https://example.test/legal/"+document.key+"/"+version,
			"/legal/"+document.key,
		); err != nil {
			return err
		}
	}
	return nil
}
