package httpapi

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	featureruntime "github.com/apdsoftware/postqron/packages/runtime"
)

func TestPostgresSessionAuthenticationAddsRuntimeAccount(t *testing.T) {
	database := sql.OpenDB(sessionAuthConnector{accountID: "account-123"})
	t.Cleanup(func() { _ = database.Close() })
	authenticate, err := NewPostgresSessionAuthentication(
		database,
		func() time.Time { return time.Unix(1_800_000_000, 0).UTC() },
	)
	if err != nil {
		t.Fatal(err)
	}

	called := false
	handler := authenticate(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		called = true
		accountID, ok := featureruntime.AuthenticatedAccount(request.Context())
		if !ok || accountID != "account-123" {
			t.Fatalf("runtime account = %q, %v", accountID, ok)
		}
		writer.WriteHeader(http.StatusNoContent)
	}))
	request := httptest.NewRequest(http.MethodGet, "/private", nil)
	request.AddCookie(&http.Cookie{
		Name:  productSessionCookie,
		Value: "valid-session-token",
	})
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if !called {
		t.Fatal("valid session did not call next")
	}
	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", response.Code)
	}
}

func TestPostgresSessionAuthenticationRejectsNoRowsWithoutCallingNext(
	t *testing.T,
) {
	database := sql.OpenDB(sessionAuthConnector{})
	t.Cleanup(func() { _ = database.Close() })
	authenticate, err := NewPostgresSessionAuthentication(database, time.Now)
	if err != nil {
		t.Fatal(err)
	}

	called := false
	handler := authenticate(http.HandlerFunc(func(
		http.ResponseWriter,
		*http.Request,
	) {
		called = true
	}))
	request := httptest.NewRequest(http.MethodGet, "/private", nil)
	request.AddCookie(&http.Cookie{
		Name:  productSessionCookie,
		Value: "unknown-session-token",
	})
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if called {
		t.Fatal("no-row session called next")
	}
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", response.Code)
	}
}

type sessionAuthConnector struct {
	accountID string
}

func (connector sessionAuthConnector) Connect(context.Context) (driver.Conn, error) {
	return sessionAuthConn{accountID: connector.accountID}, nil
}

func (connector sessionAuthConnector) Driver() driver.Driver {
	return sessionAuthDriver{accountID: connector.accountID}
}

type sessionAuthDriver struct {
	accountID string
}

func (databaseDriver sessionAuthDriver) Open(string) (driver.Conn, error) {
	return sessionAuthConn{accountID: databaseDriver.accountID}, nil
}

type sessionAuthConn struct {
	accountID string
}

func (sessionAuthConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("prepare is not supported")
}

func (sessionAuthConn) Close() error {
	return nil
}

func (sessionAuthConn) Begin() (driver.Tx, error) {
	return nil, errors.New("transactions are not supported")
}

func (connection sessionAuthConn) QueryContext(
	context.Context,
	string,
	[]driver.NamedValue,
) (driver.Rows, error) {
	return &sessionAuthRows{accountID: connection.accountID}, nil
}

type sessionAuthRows struct {
	accountID string
	sent      bool
}

func (*sessionAuthRows) Columns() []string {
	return []string{"account_id"}
}

func (*sessionAuthRows) Close() error {
	return nil
}

func (rows *sessionAuthRows) Next(values []driver.Value) error {
	if rows.sent || rows.accountID == "" {
		return io.EOF
	}
	rows.sent = true
	values[0] = rows.accountID
	return nil
}
