package adminconsole

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func directoryUser() UserDirectoryItem {
	lastLogin := testNow.Add(-time.Hour)
	return UserDirectoryItem{
		ID:             "account-user",
		Email:          "user@example.com",
		DisplayName:    "Fixture User",
		AccountStatus:  "active",
		EmailVerified:  true,
		LoginMethods:   []string{"google", "password"},
		RegisteredAt:   testNow.AddDate(0, -2, 0),
		LastLoginAt:    &lastLogin,
		ActiveSessions: 2,
		Workspaces: []UserWorkspaceMembership{{
			ID:         "workspace-1",
			Name:       "Fixture Workspace",
			Role:       "owner",
			PlanCode:   "pro",
			PlanStatus: "active",
		}},
	}
}

func directoryWorkspace() WorkspaceDirectoryItem {
	return WorkspaceDirectoryItem{
		ID:               "workspace-1",
		Name:             "Fixture Workspace",
		OwnerID:          "account-user",
		OwnerEmail:       "user@example.com",
		OwnerDisplayName: "Fixture User",
		Status:           "active",
		PlanCode:         "pro",
		PlanStatus:       "active",
		MemberCount:      3,
		ChannelCount:     4,
		PostCount:        18,
		CreatedAt:        testNow.AddDate(0, -2, 0),
		UpdatedAt:        testNow.Add(-time.Hour),
	}
}

func TestDirectoryQueriesNormalizeCombinedFiltersOrderingAndPagination(t *testing.T) {
	fixture := newServiceFixture(t, initialAdminEmail)
	fixture.reader.userPage = UserDirectoryPage{
		Items: []UserDirectoryItem{},
		Total: 0,
	}
	verified := false
	from := testNow.AddDate(0, -6, 0)
	to := testNow.AddDate(0, 0, 1)
	result, err := fixture.service.ListUsers(context.Background(), UserDirectoryQuery{
		Search:         "  fixture  ",
		Status:         "locked",
		EmailVerified:  &verified,
		Plan:           "team",
		LoginMethod:    "linkedin",
		RegisteredFrom: &from,
		RegisteredTo:   &to,
		LastLoginFrom:  &from,
		LastLoginTo:    &to,
		Page:           3,
		PageSize:       50,
		Sort:           "last_login_at",
		Direction:      "asc",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 0 || result.Total != 0 ||
		result.Page != 3 || result.PageSize != 50 {
		t.Fatalf("empty page = %+v", result)
	}
	if len(fixture.reader.userListCalls) != 1 {
		t.Fatalf("user list calls = %d", len(fixture.reader.userListCalls))
	}
	query := fixture.reader.userListCalls[0]
	if query.Search != "fixture" || query.Plan != "team" ||
		query.LoginMethod != "linkedin" || query.Direction != "asc" {
		t.Fatalf("normalized user query = %+v", query)
	}

	fixture.reader.workspacePage = WorkspaceDirectoryPage{
		Items: []WorkspaceDirectoryItem{directoryWorkspace()},
		Total: 1,
	}
	workspaces, err := fixture.service.ListWorkspaces(
		context.Background(),
		WorkspaceDirectoryQuery{
			Status: "active", Plan: "pro", Owner: " owner@example.com ",
			Page: 1, PageSize: 10, Sort: "channel_count", Direction: "desc",
			CreatedFrom: &from, CreatedTo: &to,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if workspaces.Total != 1 ||
		fixture.reader.workspaceCalls[0].Owner != "owner@example.com" {
		t.Fatalf("workspace page/query = %+v / %+v",
			workspaces, fixture.reader.workspaceCalls[0])
	}

	for _, invalid := range []UserDirectoryQuery{
		{Search: "x"},
		{Page: -1},
		{PageSize: 11},
		{Sort: "password_hash"},
		{Status: "deleted"},
		{RegisteredFrom: &to, RegisteredTo: &from},
	} {
		if _, err := fixture.service.ListUsers(context.Background(), invalid); err == nil {
			t.Fatalf("invalid query accepted: %+v", invalid)
		}
	}
}

func TestSafeUserDetailAndNotFound(t *testing.T) {
	fixture := newServiceFixture(t, initialAdminEmail)
	fixture.reader.userDetail = directoryUser()
	fixture.reader.userDetailFound = true
	result, err := fixture.service.User(context.Background(), "account-user")
	if err != nil || result.Email != "user@example.com" {
		t.Fatalf("detail = %+v, error = %v", result, err)
	}
	fixture.reader.userDetailFound = false
	if _, err := fixture.service.User(context.Background(), "missing"); err != ErrAdminNotFound {
		t.Fatalf("missing detail error = %v", err)
	}
	if _, err := fixture.service.User(context.Background(), "../unsafe"); err != ErrInvalidRequest {
		t.Fatalf("unsafe detail error = %v", err)
	}
}

func TestCSVAndXLSXExportsMitigateFormulaInjection(t *testing.T) {
	fixture := newServiceFixture(t, initialAdminEmail)
	user := directoryUser()
	user.DisplayName = "=HYPERLINK(\"https://attacker.example\")"
	fixture.reader.userPage = UserDirectoryPage{
		Items: []UserDirectoryItem{user},
		Total: 1,
	}
	query := UserDirectoryQuery{
		Page: 1, PageSize: 25, Sort: "email", Direction: "asc",
	}
	csvExport, err := fixture.service.ExportUsers(
		context.Background(),
		query,
		"csv",
	)
	if err != nil {
		t.Fatal(err)
	}
	if csvExport.ContentType != "text/csv; charset=utf-8" ||
		csvExport.Filename != "postqron-admin-users-20260725.csv" ||
		!bytes.HasPrefix(csvExport.Body, []byte("\xEF\xBB\xBF")) ||
		!strings.Contains(string(csvExport.Body), `"'=HYPERLINK`) {
		t.Fatalf("CSV export headers/body = %s %s %q",
			csvExport.ContentType, csvExport.Filename, csvExport.Body)
	}

	xlsxExport, err := fixture.service.ExportUsers(
		context.Background(),
		query,
		"xlsx",
	)
	if err != nil {
		t.Fatal(err)
	}
	archive, err := zip.NewReader(
		bytes.NewReader(xlsxExport.Body),
		int64(len(xlsxExport.Body)),
	)
	if err != nil {
		t.Fatal(err)
	}
	required := map[string]bool{
		"[Content_Types].xml":        false,
		"xl/workbook.xml":            false,
		"xl/worksheets/sheet1.xml":   false,
		"xl/_rels/workbook.xml.rels": false,
		"_rels/.rels":                false,
	}
	var worksheet string
	for _, file := range archive.File {
		if _, exists := required[file.Name]; exists {
			required[file.Name] = true
		}
		if file.Name == "xl/worksheets/sheet1.xml" {
			reader, openErr := file.Open()
			if openErr != nil {
				t.Fatal(openErr)
			}
			value, readErr := io.ReadAll(reader)
			reader.Close()
			if readErr != nil {
				t.Fatal(readErr)
			}
			worksheet = string(value)
		}
	}
	for name, found := range required {
		if !found {
			t.Fatalf("XLSX missing %s", name)
		}
	}
	if !strings.Contains(worksheet, `&#39;=HYPERLINK`) {
		t.Fatalf("XLSX formula was not escaped: %s", worksheet)
	}
}

func TestExportRejectsDatasetsOverExplicitLimit(t *testing.T) {
	fixture := newServiceFixture(t, initialAdminEmail)
	fixture.reader.workspacePage = WorkspaceDirectoryPage{
		Items: []WorkspaceDirectoryItem{},
		Total: AdminExportLimit + 1,
	}
	_, err := fixture.service.ExportWorkspaces(
		context.Background(),
		WorkspaceDirectoryQuery{
			Page: 1, PageSize: 25, Sort: "updated_at", Direction: "desc",
		},
		"csv",
	)
	if err != ErrAdminExportTooLarge {
		t.Fatalf("oversized export error = %v", err)
	}
}

func TestHTTPDirectoryEndpointsAuthorizeAndPreserveFilters(t *testing.T) {
	fixture := newServiceFixture(t, initialAdminEmail)
	fixture.reader.userPage = UserDirectoryPage{
		Items: []UserDirectoryItem{directoryUser()},
		Total: 1,
	}
	handler := testHandler(t, fixture, map[string]Session{
		"admin": validSession(),
	})
	path := "/api/v1/admin/users?q=fixture&status=active&email_verified=true" +
		"&plan=pro&login_method=password&page=2&page_size=10" +
		"&sort=email&direction=asc&registered_from=2026-01-01" +
		"&registered_to=2026-07-25"
	unauthorized := request(t, handler, http.MethodGet, path, "", nil, nil)
	if unauthorized.Code != http.StatusUnauthorized ||
		len(fixture.reader.userListCalls) != 0 {
		t.Fatalf("unauthorized directory read = %d, calls=%d",
			unauthorized.Code, len(fixture.reader.userListCalls))
	}
	response := request(t, handler, http.MethodGet, path, "admin", nil, nil)
	if response.Code != http.StatusOK ||
		!strings.Contains(response.Body.String(), `"total":1`) {
		t.Fatalf("directory response = %d %s", response.Code, response.Body)
	}
	query := fixture.reader.userListCalls[0]
	if query.Page != 2 || query.PageSize != 10 ||
		query.RegisteredFrom == nil || query.RegisteredTo == nil ||
		query.RegisteredTo.Sub(*query.RegisteredFrom) != 206*24*time.Hour {
		t.Fatalf("HTTP filters = %+v", query)
	}

	fixture.reader.workspacePage = WorkspaceDirectoryPage{
		Items: []WorkspaceDirectoryItem{directoryWorkspace()},
		Total: 1,
	}
	export := request(
		t,
		handler,
		http.MethodGet,
		"/api/v1/admin/workspaces/export?format=xlsx&status=active&plan=pro",
		"admin",
		nil,
		nil,
	)
	if export.Code != http.StatusOK ||
		export.Header().Get("Content-Type") !=
			"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet" ||
		export.Header().Get("Content-Disposition") !=
			`attachment; filename="postqron-admin-workspaces-20260725.xlsx"` {
		t.Fatalf("export response = %d %v", export.Code, export.Header())
	}
}
