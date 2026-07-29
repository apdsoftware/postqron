package auth

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type passwordSQLStep struct {
	kind     string
	contains string
	columns  []string
	rows     [][]driver.Value
	affected int64
}

type passwordSQLState struct {
	mu        sync.Mutex
	steps     []passwordSQLStep
	index     int
	committed bool
}

type passwordSQLDriver struct {
	state *passwordSQLState
}

type passwordSQLConnection struct {
	state *passwordSQLState
}

type passwordSQLTransaction struct {
	state *passwordSQLState
}

type passwordSQLRows struct {
	columns []string
	rows    [][]driver.Value
	index   int
}

var passwordSQLDriverID atomic.Uint64

func (driverFixture *passwordSQLDriver) Open(string) (driver.Conn, error) {
	return &passwordSQLConnection{state: driverFixture.state}, nil
}

func (connection *passwordSQLConnection) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("prepared statements are not supported")
}

func (connection *passwordSQLConnection) Close() error {
	return nil
}

func (connection *passwordSQLConnection) Begin() (driver.Tx, error) {
	return &passwordSQLTransaction{state: connection.state}, nil
}

func (connection *passwordSQLConnection) BeginTx(
	context.Context,
	driver.TxOptions,
) (driver.Tx, error) {
	return &passwordSQLTransaction{state: connection.state}, nil
}

func (connection *passwordSQLConnection) QueryContext(
	_ context.Context,
	query string,
	_ []driver.NamedValue,
) (driver.Rows, error) {
	step, err := connection.state.next("query", query)
	if err != nil {
		return nil, err
	}
	return &passwordSQLRows{
		columns: step.columns,
		rows:    step.rows,
	}, nil
}

func (connection *passwordSQLConnection) ExecContext(
	_ context.Context,
	query string,
	_ []driver.NamedValue,
) (driver.Result, error) {
	step, err := connection.state.next("exec", query)
	if err != nil {
		return nil, err
	}
	return driver.RowsAffected(step.affected), nil
}

func (transaction *passwordSQLTransaction) Commit() error {
	transaction.state.mu.Lock()
	defer transaction.state.mu.Unlock()
	transaction.state.committed = true
	return nil
}

func (*passwordSQLTransaction) Rollback() error {
	return nil
}

func (rows *passwordSQLRows) Columns() []string {
	return rows.columns
}

func (*passwordSQLRows) Close() error {
	return nil
}

func (rows *passwordSQLRows) Next(destination []driver.Value) error {
	if rows.index >= len(rows.rows) {
		return io.EOF
	}
	copy(destination, rows.rows[rows.index])
	rows.index++
	return nil
}

func (state *passwordSQLState) next(
	kind, query string,
) (passwordSQLStep, error) {
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.index >= len(state.steps) {
		return passwordSQLStep{}, fmt.Errorf("unexpected %s: %s", kind, query)
	}
	step := state.steps[state.index]
	state.index++
	if step.kind != kind || !strings.Contains(query, step.contains) {
		return passwordSQLStep{}, fmt.Errorf(
			"SQL step %d = %s %q, want %s containing %q",
			state.index,
			kind,
			query,
			step.kind,
			step.contains,
		)
	}
	return step, nil
}

func openPasswordTestDatabase(
	t *testing.T,
	steps ...passwordSQLStep,
) (*sql.DB, *passwordSQLState) {
	t.Helper()
	state := &passwordSQLState{steps: steps}
	name := fmt.Sprintf(
		"postqron-password-script-%d",
		passwordSQLDriverID.Add(1),
	)
	sql.Register(name, &passwordSQLDriver{state: state})
	database, err := sql.Open(name, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = database.Close()
	})
	return database, state
}

func assertPasswordSQLComplete(t *testing.T, state *passwordSQLState) {
	t.Helper()
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.index != len(state.steps) {
		t.Fatalf("executed %d of %d SQL steps", state.index, len(state.steps))
	}
	if !state.committed {
		t.Fatal("password persistence transaction was not committed")
	}
}

func TestPostgresPasswordChangeIsOneAtomicRotation(t *testing.T) {
	database, state := openPasswordTestDatabase(
		t,
		passwordSQLStep{
			kind:     "query",
			contains: "SELECT access_state",
			columns:  []string{"access_state"},
			rows:     [][]driver.Value{{"active"}},
		},
		passwordSQLStep{
			kind:     "query",
			contains: "SELECT password_hash",
			columns:  []string{"password_hash"},
			rows:     [][]driver.Value{{"current-argon2id-hash"}},
		},
		passwordSQLStep{
			kind:     "query",
			contains: "FROM auth_sessions",
			columns:  []string{"account_id"},
			rows:     [][]driver.Value{{"account-admin"}},
		},
		passwordSQLStep{
			kind:     "exec",
			contains: "UPDATE auth_password_credentials",
			affected: 1,
		},
		passwordSQLStep{
			kind:     "exec",
			contains: "UPDATE auth_sessions",
			affected: 3,
		},
		passwordSQLStep{
			kind:     "exec",
			contains: "INSERT INTO auth_sessions",
			affected: 1,
		},
		passwordSQLStep{
			kind:     "exec",
			contains: "'password.changed'",
			affected: 1,
		},
	)
	store, err := NewPostgresPasswordStore(database)
	if err != nil {
		t.Fatal(err)
	}
	err = store.CompletePasswordChange(
		context.Background(),
		PasswordChange{
			AccountID:               "account-admin",
			CurrentPasswordHash:     "current-argon2id-hash",
			CurrentSessionTokenHash: strings.Repeat("a", 64),
			NewPasswordHash:         "new-argon2id-hash",
			NewSession: PasswordSession{
				ID:              "session-rotated",
				AccountID:       "account-admin",
				TokenHash:       strings.Repeat("b", 64),
				CreatedAt:       testNow,
				AuthenticatedAt: testNow,
				ExpiresAt:       testNow.Add(time.Hour),
			},
		},
		"event-password-changed",
		testNow,
	)
	if err != nil {
		t.Fatalf("CompletePasswordChange() error = %v", err)
	}
	assertPasswordSQLComplete(t, state)
}

func TestPostgresLogoutRevokesBeforeAuditing(t *testing.T) {
	database, state := openPasswordTestDatabase(
		t,
		passwordSQLStep{
			kind:     "query",
			contains: "UPDATE auth_sessions",
			columns:  []string{"account_id"},
			rows:     [][]driver.Value{{"account-admin"}},
		},
		passwordSQLStep{
			kind:     "exec",
			contains: "'session.logged_out'",
			affected: 1,
		},
	)
	store, err := NewPostgresPasswordStore(database)
	if err != nil {
		t.Fatal(err)
	}
	err = store.RevokePasswordSession(
		context.Background(),
		strings.Repeat("a", 64),
		"event-session-logout",
		testNow,
	)
	if err != nil {
		t.Fatalf("RevokePasswordSession() error = %v", err)
	}
	assertPasswordSQLComplete(t, state)
}

func TestPostgresPasswordChangeRejectsConcurrentCredentialUpdate(t *testing.T) {
	database, state := openPasswordTestDatabase(
		t,
		passwordSQLStep{
			kind:     "query",
			contains: "SELECT access_state",
			columns:  []string{"access_state"},
			rows:     [][]driver.Value{{"active"}},
		},
		passwordSQLStep{
			kind:     "query",
			contains: "SELECT password_hash",
			columns:  []string{"password_hash"},
			rows:     [][]driver.Value{{"a-concurrently-updated-hash"}},
		},
	)
	store, err := NewPostgresPasswordStore(database)
	if err != nil {
		t.Fatal(err)
	}
	err = store.CompletePasswordChange(
		context.Background(),
		PasswordChange{
			AccountID:           "account-admin",
			CurrentPasswordHash: "the-original-hash",
		},
		"event-must-not-be-written",
		testNow,
	)
	if !errors.Is(err, ErrPasswordChangeConflict) {
		t.Fatalf("CompletePasswordChange() error = %v", err)
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.index != 2 || state.committed {
		t.Fatalf(
			"concurrent change executed %d steps, committed=%v",
			state.index,
			state.committed,
		)
	}
}

func TestPostgresPasswordLoginFailsClosedWhenAccountFrozen(t *testing.T) {
	database, state := openPasswordTestDatabase(
		t,
		passwordSQLStep{
			kind:     "query",
			contains: "SELECT access_state",
			columns:  []string{"access_state"},
			rows:     [][]driver.Value{{"frozen"}},
		},
	)
	store, err := NewPostgresPasswordStore(database)
	if err != nil {
		t.Fatal(err)
	}
	err = store.CompletePasswordLogin(
		context.Background(),
		PasswordSession{AccountID: "account-frozen"},
		"event-must-not-be-written",
		testNow,
	)
	if !errors.Is(err, ErrAccountAccessUnavailable) {
		t.Fatalf("CompletePasswordLogin() error = %v", err)
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.index != 1 || state.committed {
		t.Fatalf("frozen login executed %d steps, committed=%v", state.index, state.committed)
	}
}
