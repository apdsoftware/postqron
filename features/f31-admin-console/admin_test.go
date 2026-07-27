package adminconsole

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

const (
	initialAdminEmail = "carlo.zuffetti@apdsoftware.it"
	initialAdminID    = "account-carlo"
)

type readerStub struct {
	dashboardCalls  int
	searchCalls     int
	planCalls       int
	auditCalls      int
	detailCalls     int
	plans           []PlanRow
	auditEvents     []AuditEvent
	planTotal       int
	auditTotal      int
	lastPlanQuery   PlanQuery
	lastAuditQuery  AuditQuery
	lastPlanPage    PageRequest
	lastAuditPage   PageRequest
	userListCalls   []UserDirectoryQuery
	workspaceCalls  []WorkspaceDirectoryQuery
	userPage        UserDirectoryPage
	workspacePage   WorkspaceDirectoryPage
	userDetail      UserDirectoryItem
	userDetailFound bool
}

func (reader *readerStub) Dashboard(context.Context) (Dashboard, error) {
	reader.dashboardCalls++
	return Dashboard{
		Services: []ServiceHealth{{
			Code:      "api",
			Status:    "healthy",
			CheckedAt: testNow,
		}},
		Entitlements: []EntitlementSummary{{
			WorkspaceID: "workspace-1",
			PlanCode:    "pro",
		}},
		RecentAudit: []AuditEvent{},
	}, nil
}

func (reader *readerStub) Search(context.Context, string) (SearchResults, error) {
	reader.searchCalls++
	return SearchResults{}, nil
}

func (reader *readerStub) ListPlans(
	_ context.Context,
	query PlanQuery,
	page PageRequest,
) (PlanPage, error) {
	reader.planCalls++
	reader.lastPlanQuery = query
	reader.lastPlanPage = page
	total := reader.planTotal
	if total == 0 {
		total = len(reader.plans)
	}
	start := (page.Page - 1) * page.PageSize
	if start >= len(reader.plans) {
		return PlanPage{Items: []PlanRow{}, Total: total}, nil
	}
	end := min(start+page.PageSize, len(reader.plans))
	return PlanPage{
		Items: append([]PlanRow(nil), reader.plans[start:end]...),
		Total: total,
	}, nil
}

func (reader *readerStub) ListAudit(
	_ context.Context,
	query AuditQuery,
	page PageRequest,
) (AuditPage, error) {
	reader.auditCalls++
	reader.lastAuditQuery = query
	reader.lastAuditPage = page
	total := reader.auditTotal
	if total == 0 {
		total = len(reader.auditEvents)
	}
	start := (page.Page - 1) * page.PageSize
	if start >= len(reader.auditEvents) {
		return AuditPage{Items: []AuditEvent{}, Total: total}, nil
	}
	end := min(start+page.PageSize, len(reader.auditEvents))
	return AuditPage{
		Items: append([]AuditEvent(nil), reader.auditEvents[start:end]...),
		Total: total,
	}, nil
}

func (reader *readerStub) AuditEvent(
	_ context.Context,
	eventID string,
) (AuditEvent, bool, error) {
	reader.detailCalls++
	for _, event := range reader.auditEvents {
		if event.ID == eventID {
			return event, true, nil
		}
	}
	return AuditEvent{}, false, nil
}

func (reader *readerStub) ListUsers(
	_ context.Context,
	query UserDirectoryQuery,
) (UserDirectoryPage, error) {
	reader.userListCalls = append(reader.userListCalls, query)
	return reader.userPage, nil
}

func (reader *readerStub) User(
	context.Context,
	string,
) (UserDirectoryItem, bool, error) {
	return reader.userDetail, reader.userDetailFound, nil
}

func (reader *readerStub) ListWorkspaces(
	_ context.Context,
	query WorkspaceDirectoryQuery,
) (WorkspaceDirectoryPage, error) {
	reader.workspaceCalls = append(reader.workspaceCalls, query)
	return reader.workspacePage, nil
}

type planStub struct {
	changes []InternalPlanChange
	err     error
}

type failingAudit struct{}

func (failingAudit) Append(context.Context, AuditEvent) error {
	return errors.New("audit offline")
}

func (plan *planStub) Change(_ context.Context, change InternalPlanChange) error {
	if plan.err != nil {
		return plan.err
	}
	plan.changes = append(plan.changes, change)
	return nil
}

var testNow = time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)

type serviceFixture struct {
	service   *Service
	directory *MemoryDirectory
	audit     *MemoryAudit
	reader    *readerStub
	plan      *planStub
}

func newServiceFixture(t *testing.T, allowlist ...string) serviceFixture {
	t.Helper()
	directory := NewMemoryDirectory(map[string]string{
		initialAdminID: initialAdminEmail,
		"account-two":  "second.admin@apdsoftware.it",
		"account-user": "user@example.com",
	})
	audit := &MemoryAudit{}
	reader := &readerStub{}
	plan := &planStub{}
	id := 0
	service, err := NewService(Config{
		Allowlist:    allowlist,
		Directory:    directory,
		Reader:       reader,
		Browser:      reader,
		InternalPlan: plan,
		Audit:        audit,
		Idempotency:  NewMemoryIdempotencyStore(),
		Now:          func() time.Time { return testNow },
		NewID: func() string {
			id++
			return fmt.Sprintf("event-%d", id)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.BootstrapAdmins(context.Background()); err != nil {
		t.Fatal(err)
	}
	return serviceFixture{
		service: service, directory: directory, audit: audit,
		reader: reader, plan: plan,
	}
}

func validSession() Session {
	return Session{
		AccountID:       initialAdminID,
		Email:           "  CARLO.ZUFFETTI@APDSOFTWARE.IT ",
		EmailVerified:   true,
		AuthenticatedAt: testNow.Add(-time.Minute),
		ExpiresAt:       testNow.Add(time.Hour),
		CSRFToken:       "csrf-test-token",
	}
}

func TestAuthorizationRequiresValidVerifiedAllowlistedActiveSession(t *testing.T) {
	fixture := newServiceFixture(t, initialAdminEmail)

	principal, err := fixture.service.Authorize(context.Background(), validSession())
	if err != nil {
		t.Fatal(err)
	}
	if principal.Email != initialAdminEmail || principal.AccountID != initialAdminID {
		t.Fatalf("principal = %+v", principal)
	}

	tests := []struct {
		name    string
		session Session
		want    error
	}{
		{
			name: "unverified email",
			session: func() Session {
				value := validSession()
				value.EmailVerified = false
				return value
			}(),
			want: ErrForbidden,
		},
		{
			name: "normal user",
			session: Session{
				AccountID: "account-user", Email: "user@example.com",
				EmailVerified: true, AuthenticatedAt: testNow,
				ExpiresAt: testNow.Add(time.Hour),
			},
			want: ErrForbidden,
		},
		{
			name: "expired session",
			session: func() Session {
				value := validSession()
				value.ExpiresAt = testNow
				return value
			}(),
			want: ErrUnauthenticated,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := fixture.service.Authorize(context.Background(), test.session)
			if !errors.Is(err, test.want) {
				t.Fatalf("Authorize() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestInitialAdministratorComesFromNormalizedServerConfiguration(t *testing.T) {
	allowlist, err := AllowlistFromEnvironment(func(key string) (string, bool) {
		if key != AdminAllowlistEnvName {
			return "", false
		}
		return "  CARLO.ZUFFETTI@APDSOFTWARE.IT  ", true
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(allowlist) != 1 || allowlist[0] != initialAdminEmail {
		t.Fatalf("allowlist = %v", allowlist)
	}
	if _, err := AllowlistFromEnvironment(func(string) (string, bool) {
		return "", false
	}); err == nil {
		t.Fatal("missing server allowlist did not fail closed")
	}
}

func TestPlanAndAuditListsValidateCombinedFiltersAndServerPagination(t *testing.T) {
	fixture := newServiceFixture(t, initialAdminEmail)
	from := testNow.Add(-24 * time.Hour)
	to := testNow.Add(time.Hour)
	fixture.reader.plans = []PlanRow{
		{WorkspaceID: "workspace-1"},
		{WorkspaceID: "workspace-2"},
		{WorkspaceID: "workspace-3"},
	}
	plans, err := fixture.service.Plans(
		context.Background(),
		PlanQuery{
			Search: " Studio ",
			Plan:   "pro", Status: "active", Type: "internal",
			From: &from, To: &to, Sort: "owner", Direction: "asc",
		},
		PageRequest{Page: 2, PageSize: 2},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(plans.Items) != 1 || plans.Items[0].WorkspaceID != "workspace-3" ||
		plans.Pagination.Total != 3 || plans.Pagination.Page != 2 {
		t.Fatalf("plans = %+v", plans)
	}
	if fixture.reader.lastPlanQuery.Search != "Studio" ||
		fixture.reader.lastPlanQuery.Type != "internal" ||
		fixture.reader.lastPlanPage != (PageRequest{Page: 2, PageSize: 2}) {
		t.Fatalf(
			"plan query/page = %+v / %+v",
			fixture.reader.lastPlanQuery,
			fixture.reader.lastPlanPage,
		)
	}

	fixture.reader.auditEvents = []AuditEvent{{
		ID: "audit-event-1", Code: "internal_plan.assign",
		ActorID: "account-admin", SubjectID: "workspace-1",
		Outcome: "succeeded", Reason: "approved operation",
		CorrelationID: "correlation-1", OccurredAt: testNow,
	}}
	audit, err := fixture.service.Audit(
		context.Background(),
		AuditQuery{
			Action: " internal_plan.assign ", Actor: "account",
			Subject: "workspace", Outcome: "succeeded",
			From: &from, To: &to, Sort: "code", Direction: "asc",
		},
		PageRequest{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(audit.Items) != 1 ||
		fixture.reader.lastAuditQuery.Action != "internal_plan.assign" ||
		fixture.reader.lastAuditPage.PageSize != defaultAdminPageSize {
		t.Fatalf("audit = %+v, query = %+v", audit, fixture.reader.lastAuditQuery)
	}
	detail, err := fixture.service.AuditDetail(context.Background(), "audit-event-1")
	if err != nil || detail.CorrelationID != "correlation-1" {
		t.Fatalf("detail = %+v, error = %v", detail, err)
	}

	invalidQueries := []func() error{
		func() error {
			_, err := fixture.service.Plans(
				context.Background(),
				PlanQuery{Sort: "secret_column"},
				PageRequest{},
			)
			return err
		},
		func() error {
			_, err := fixture.service.Plans(
				context.Background(),
				PlanQuery{From: &to, To: &from},
				PageRequest{},
			)
			return err
		},
		func() error {
			_, err := fixture.service.Audit(
				context.Background(),
				AuditQuery{Direction: "sideways"},
				PageRequest{PageSize: 101},
			)
			return err
		},
	}
	for _, operation := range invalidQueries {
		if err := operation(); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("invalid query error = %v", err)
		}
	}
}

func TestExportsEnforceExplicitWholeResultLimit(t *testing.T) {
	fixture := newServiceFixture(t, initialAdminEmail)
	fixture.reader.planTotal = maxAdminExportRows + 1
	if _, err := fixture.service.ExportPlans(
		context.Background(),
		PlanQuery{},
	); !errors.Is(err, ErrExportLimitExceeded) {
		t.Fatalf("plan export error = %v", err)
	}
	fixture.reader.auditTotal = maxAdminExportRows + 1
	if _, err := fixture.service.ExportAudit(
		context.Background(),
		AuditQuery{},
	); !errors.Is(err, ErrExportLimitExceeded) {
		t.Fatalf("audit export error = %v", err)
	}
	if fixture.reader.lastPlanPage.PageSize != maxAdminExportRows+1 ||
		fixture.reader.lastAuditPage.PageSize != maxAdminExportRows+1 {
		t.Fatalf(
			"export page limits = %d/%d",
			fixture.reader.lastPlanPage.PageSize,
			fixture.reader.lastAuditPage.PageSize,
		)
	}
}

func TestAuditStorageMigrationRemainsAppendOnly(t *testing.T) {
	migration, err := os.ReadFile("migrations/000001_create_admin_console.sql")
	if err != nil {
		t.Fatal(err)
	}
	source := string(migration)
	for _, invariant := range []string{
		"BEFORE UPDATE OR DELETE ON f31_admin_audit_events",
		"ENABLE ALWAYS TRIGGER f31_admin_audit_events_append_only",
		"REVOKE UPDATE, DELETE, TRUNCATE ON f31_admin_audit_events FROM PUBLIC",
	} {
		if !strings.Contains(source, invariant) {
			t.Fatalf("append-only audit invariant missing: %s", invariant)
		}
	}
}

func TestAdminReconciliationIsConfiguredAndAppendOnlyAudited(t *testing.T) {
	fixture := newServiceFixture(
		t,
		initialAdminEmail,
		"second.admin@apdsoftware.it",
	)
	second, exists, err := fixture.directory.Admin(context.Background(), "account-two")
	if err != nil || !exists || !second.Active {
		t.Fatalf("second admin = %+v, exists=%v, error=%v", second, exists, err)
	}
	events := fixture.audit.Events()
	if len(events) != 2 {
		t.Fatalf("audit events = %d, want 2", len(events))
	}
	events[0].Reason = "tampered copy"
	if fixture.audit.Events()[0].Reason == "tampered copy" {
		t.Fatal("audit history exposed mutable backing storage")
	}
}

func TestSensitiveMutationChecksCSRFReauthConfirmationAndIdempotency(t *testing.T) {
	fixture := newServiceFixture(t, initialAdminEmail)
	session := validSession()
	principal, err := fixture.service.Authorize(context.Background(), session)
	if err != nil {
		t.Fatal(err)
	}
	request := MutationRequest{
		Action:         "internal_plan.assign",
		SubjectID:      "workspace-1",
		Reason:         "Approved for internal operations",
		Confirmed:      true,
		CSRFToken:      session.CSRFToken,
		IdempotencyKey: "request-00000001",
	}
	first, err := fixture.service.Mutate(context.Background(), principal, session, request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := fixture.service.Mutate(context.Background(), principal, session, request)
	if err != nil {
		t.Fatal(err)
	}
	if first != second || len(fixture.plan.changes) != 1 {
		t.Fatalf("results = %+v/%+v, changes = %d", first, second, len(fixture.plan.changes))
	}
	if fixture.plan.changes[0].Reason != request.Reason {
		t.Fatalf("F11 change omitted reason: %+v", fixture.plan.changes[0])
	}
	events := fixture.audit.Events()
	if len(events) != 2 ||
		events[1].Code != "internal_plan.assign" ||
		events[1].Reason != request.Reason {
		t.Fatalf("internal plan audit = %+v", events)
	}

	cases := []struct {
		name    string
		session Session
		request MutationRequest
		want    error
	}{
		{
			name:    "csrf",
			session: session,
			request: func() MutationRequest {
				value := request
				value.IdempotencyKey = "request-00000002"
				value.CSRFToken = "forged"
				return value
			}(),
			want: ErrCSRF,
		},
		{
			name: "reauth",
			session: func() Session {
				value := session
				value.AuthenticatedAt = testNow.Add(-6 * time.Minute)
				return value
			}(),
			request: func() MutationRequest {
				value := request
				value.IdempotencyKey = "request-00000003"
				return value
			}(),
			want: ErrRecentReauthRequired,
		},
		{
			name:    "confirmation",
			session: session,
			request: func() MutationRequest {
				value := request
				value.IdempotencyKey = "request-00000004"
				value.Confirmed = false
				return value
			}(),
			want: ErrInvalidRequest,
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			_, err := fixture.service.Mutate(
				context.Background(),
				principal,
				test.session,
				test.request,
			)
			if !errors.Is(err, test.want) {
				t.Fatalf("Mutate() error = %v, want %v", err, test.want)
			}
		})
	}
	if len(fixture.plan.changes) != 1 {
		t.Fatalf("rejected manipulations reached F11: %d changes", len(fixture.plan.changes))
	}
}

func TestPayloadCannotPromoteAccountOutsideServerConfiguration(t *testing.T) {
	fixture := newServiceFixture(t, initialAdminEmail)
	session := validSession()
	principal, err := fixture.service.Authorize(context.Background(), session)
	if err != nil {
		t.Fatal(err)
	}
	_, err = fixture.service.Mutate(
		context.Background(),
		principal,
		session,
		MutationRequest{
			Action:         "admin.add",
			SubjectID:      "account-user",
			SubjectEmail:   "user@example.com",
			Reason:         "Attempted client-side promotion",
			Confirmed:      true,
			CSRFToken:      session.CSRFToken,
			IdempotencyKey: "request-00000005",
		},
	)
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("Mutate() error = %v, want forbidden", err)
	}
	record, _, err := fixture.directory.Admin(context.Background(), "account-user")
	if err != nil {
		t.Fatal(err)
	}
	if record.Active {
		t.Fatal("client payload promoted a non-configured account")
	}
}

func TestMutationRechecksCurrentServerAuthorization(t *testing.T) {
	fixture := newServiceFixture(t, initialAdminEmail)
	session := validSession()
	principal, err := fixture.service.Authorize(context.Background(), session)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.directory.SetAdmin(context.Background(), initialAdminID, false); err != nil {
		t.Fatal(err)
	}
	_, err = fixture.service.Mutate(
		context.Background(),
		principal,
		session,
		MutationRequest{
			Action:         "internal_plan.assign",
			SubjectID:      "workspace-1",
			Reason:         "Attempt after authorization revocation",
			Confirmed:      true,
			CSRFToken:      session.CSRFToken,
			IdempotencyKey: "request-00000006",
		},
	)
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("Mutate() error = %v, want forbidden", err)
	}
	if len(fixture.plan.changes) != 0 {
		t.Fatal("revoked administrator reached the F11 mutation")
	}
}

func TestConfiguredAdminAdditionAndRemovalAreAudited(t *testing.T) {
	fixture := newServiceFixture(
		t,
		initialAdminEmail,
		"second.admin@apdsoftware.it",
	)
	session := validSession()
	principal, err := fixture.service.Authorize(context.Background(), session)
	if err != nil {
		t.Fatal(err)
	}
	for index, action := range []string{"admin.remove", "admin.add"} {
		_, err := fixture.service.Mutate(
			context.Background(),
			principal,
			session,
			MutationRequest{
				Action:         action,
				SubjectID:      "account-two",
				SubjectEmail:   "SECOND.ADMIN@APDSOFTWARE.IT",
				Reason:         "Approved administrator lifecycle change",
				Confirmed:      true,
				CSRFToken:      session.CSRFToken,
				IdempotencyKey: fmt.Sprintf("admin-change-%08d", index),
			},
		)
		if err != nil {
			t.Fatalf("%s: %v", action, err)
		}
	}
	record, _, err := fixture.directory.Admin(context.Background(), "account-two")
	if err != nil {
		t.Fatal(err)
	}
	if !record.Active {
		t.Fatal("configured administrator was not reactivated")
	}
	events := fixture.audit.Events()
	if len(events) != 4 ||
		events[2].Code != "admin.remove" ||
		events[3].Code != "admin.add" {
		t.Fatalf("admin audit events = %+v", events)
	}
}

func TestAuditFailureBlocksInternalPlanMutation(t *testing.T) {
	directory := NewMemoryDirectory(map[string]string{
		initialAdminID: initialAdminEmail,
	})
	if err := directory.SetAdmin(context.Background(), initialAdminID, true); err != nil {
		t.Fatal(err)
	}
	plan := &planStub{}
	service, err := NewService(Config{
		Allowlist:    []string{initialAdminEmail},
		Directory:    directory,
		Reader:       &readerStub{},
		Browser:      &readerStub{},
		InternalPlan: plan,
		Audit:        failingAudit{},
		Idempotency:  NewMemoryIdempotencyStore(),
		Now:          func() time.Time { return testNow },
		NewID:        func() string { return "event-audit-failure" },
	})
	if err != nil {
		t.Fatal(err)
	}
	session := validSession()
	principal, err := service.Authorize(context.Background(), session)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Mutate(
		context.Background(),
		principal,
		session,
		MutationRequest{
			Action:         "internal_plan.assign",
			SubjectID:      "workspace-1",
			Reason:         "Required immutable audit is offline",
			Confirmed:      true,
			CSRFToken:      session.CSRFToken,
			IdempotencyKey: "audit-failure-request",
		},
	)
	if !errors.Is(err, ErrAuditUnavailable) {
		t.Fatalf("Mutate() error = %v, want audit unavailable", err)
	}
	if len(plan.changes) != 0 {
		t.Fatal("plan changed without an immutable audit record")
	}
}
