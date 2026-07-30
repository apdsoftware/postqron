package runner

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	workspaces "github.com/apdsoftware/postqron/features/f04-workspaces"
	featureruntime "github.com/apdsoftware/postqron/packages/runtime"
)

type runnerSQLStep struct {
	kind     string
	contains string
	columns  []string
	rows     [][]driver.Value
	affected int64
	check    func([]driver.NamedValue) error
}

type runnerSQLState struct {
	mu        sync.Mutex
	steps     []runnerSQLStep
	index     int
	committed bool
}

type runnerSQLDriver struct {
	state *runnerSQLState
}

type runnerSQLConnection struct {
	state *runnerSQLState
}

type runnerSQLTransaction struct {
	state *runnerSQLState
}

type runnerSQLRows struct {
	columns []string
	rows    [][]driver.Value
	index   int
}

var runnerSQLDriverID atomic.Uint64

func (driverFixture *runnerSQLDriver) Open(string) (driver.Conn, error) {
	return &runnerSQLConnection{state: driverFixture.state}, nil
}

func (*runnerSQLConnection) Prepare(string) (driver.Stmt, error) {
	return nil, fmt.Errorf("prepared statements are not supported")
}

func (*runnerSQLConnection) Close() error {
	return nil
}

func (connection *runnerSQLConnection) Begin() (driver.Tx, error) {
	return &runnerSQLTransaction{state: connection.state}, nil
}

func (connection *runnerSQLConnection) BeginTx(
	context.Context,
	driver.TxOptions,
) (driver.Tx, error) {
	return &runnerSQLTransaction{state: connection.state}, nil
}

func (connection *runnerSQLConnection) QueryContext(
	_ context.Context,
	query string,
	args []driver.NamedValue,
) (driver.Rows, error) {
	step, err := connection.state.next("query", query, args)
	if err != nil {
		return nil, err
	}
	return &runnerSQLRows{
		columns: step.columns,
		rows:    step.rows,
	}, nil
}

func (connection *runnerSQLConnection) ExecContext(
	_ context.Context,
	query string,
	args []driver.NamedValue,
) (driver.Result, error) {
	step, err := connection.state.next("exec", query, args)
	if err != nil {
		return nil, err
	}
	return driver.RowsAffected(step.affected), nil
}

func (transaction *runnerSQLTransaction) Commit() error {
	transaction.state.mu.Lock()
	defer transaction.state.mu.Unlock()
	transaction.state.committed = true
	return nil
}

func (*runnerSQLTransaction) Rollback() error {
	return nil
}

func (rows *runnerSQLRows) Columns() []string {
	return rows.columns
}

func (*runnerSQLRows) Close() error {
	return nil
}

func (rows *runnerSQLRows) Next(destination []driver.Value) error {
	if rows.index >= len(rows.rows) {
		return io.EOF
	}
	copy(destination, rows.rows[rows.index])
	rows.index++
	return nil
}

func (state *runnerSQLState) next(
	kind, query string,
	args []driver.NamedValue,
) (runnerSQLStep, error) {
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.index >= len(state.steps) {
		return runnerSQLStep{}, fmt.Errorf("unexpected %s: %s", kind, query)
	}
	step := state.steps[state.index]
	state.index++
	if step.kind != kind || !strings.Contains(query, step.contains) {
		return runnerSQLStep{}, fmt.Errorf(
			"SQL step %d = %s %q, want %s containing %q",
			state.index,
			kind,
			query,
			step.kind,
			step.contains,
		)
	}
	if step.check != nil {
		if err := step.check(args); err != nil {
			return runnerSQLStep{}, err
		}
	}
	return step, nil
}

func openRunnerTestDatabase(
	t *testing.T,
	steps ...runnerSQLStep,
) (*sql.DB, *runnerSQLState) {
	t.Helper()
	state := &runnerSQLState{steps: steps}
	name := fmt.Sprintf(
		"postqron-runner-script-%d",
		runnerSQLDriverID.Add(1),
	)
	sql.Register(name, &runnerSQLDriver{state: state})
	database, err := sql.Open(name, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = database.Close()
	})
	return database, state
}

func assertRunnerSQLComplete(t *testing.T, state *runnerSQLState) {
	t.Helper()
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.index != len(state.steps) {
		t.Fatalf("executed %d of %d SQL steps", state.index, len(state.steps))
	}
}

func TestTickRunsDiscoveredFeatures(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))
	features := []featureruntime.Feature{{
		Manifest: featureruntime.Manifest{
			ID:      "runtime",
			Version: "0.1.0",
		},
	}}

	New(features, time.Second, logger).Tick(context.Background())

	if got := output.String(); !strings.Contains(got, `"feature":"runtime"`) {
		t.Fatalf("Tick() output = %q, want runtime feature", got)
	}
}

func TestTickHonorsCancelledContext(t *testing.T) {
	var output bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	New(
		[]featureruntime.Feature{{Manifest: featureruntime.Manifest{ID: "runtime"}}},
		time.Second,
		slog.New(slog.NewJSONHandler(&output, nil)),
	).Tick(ctx)

	if output.Len() != 0 {
		t.Fatalf("Tick() output = %q, want none", output.String())
	}
}

func TestProcessOnboardingEventProvisionsProfileAndTrialBeforePublishing(t *testing.T) {
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	eventTime := now.Add(-time.Minute)
	payload := []byte(`{
		"account_id":"account-1",
		"email":"fresh.user@example.test",
		"display_name":"",
		"contract_country":"IT",
		"personal_workspace_key":"personal:account-1",
		"requested_role":"owner",
		"idempotency_key":"auth-account:account-1",
		"occurred_at":"2026-07-30T11:59:00Z"
	}`)
	database, state := openRunnerTestDatabase(
		t,
		runnerSQLStep{
			kind:     "query",
			contains: "UPDATE auth_outbox_events event",
			columns:  []string{"id", "payload"},
			rows:     [][]driver.Value{{"event-1", payload}},
		},
		runnerSQLStep{
			kind:     "exec",
			contains: "INSERT INTO account_privacy_profiles",
			affected: 1,
			check: func(args []driver.NamedValue) error {
				if args[0].Value != "account-1" || args[1].Value != "fresh.user" || args[2].Value != now {
					return fmt.Errorf("unexpected profile args: %#v", args)
				}
				return nil
			},
		},
		runnerSQLStep{
			kind:     "query",
			contains: "SELECT f10_provision_trial",
			columns:  []string{"f10_provision_trial"},
			rows:     [][]driver.Value{{true}},
			check: func(args []driver.NamedValue) error {
				if args[0].Value != "workspace-1" || args[1].Value != eventTime {
					return fmt.Errorf("unexpected trial args: %#v", args)
				}
				return nil
			},
		},
		runnerSQLStep{
			kind:     "exec",
			contains: "SET published_at = $2",
			affected: 1,
		},
	)
	previousFactory := newWorkspaceRuntimeService
	t.Cleanup(func() { newWorkspaceRuntimeService = previousFactory })
	newWorkspaceRuntimeService = func(*sql.DB, func() time.Time) (workspaceOnboardingRuntime, error) {
		return onboardingRuntimeStub{
			workspace: workspaces.Workspace{ID: "workspace-1", Name: "fresh.user's workspace"},
		}, nil
	}

	processed, err := (&Runner{database: database, clock: func() time.Time { return now }}).
		processOnboardingEvent(context.Background())
	if err != nil {
		t.Fatalf("processOnboardingEvent() error = %v", err)
	}
	if !processed {
		t.Fatal("processOnboardingEvent() = false, want true")
	}
	assertRunnerSQLComplete(t, state)
}

func TestProcessOnboardingEventRetryUsesExistingWorkspaceForTrialAndProfileRepair(t *testing.T) {
	now := time.Date(2026, 7, 30, 13, 0, 0, 0, time.UTC)
	eventTime := now.Add(-2 * time.Minute)
	payload := []byte(`{
		"account_id":"account-2",
		"email":"retry@example.test",
		"display_name":"Retry User",
		"contract_country":"IT",
		"personal_workspace_key":"personal:account-2",
		"requested_role":"owner",
		"idempotency_key":"auth-account:account-2",
		"occurred_at":"2026-07-30T12:58:00Z"
	}`)
	database, state := openRunnerTestDatabase(
		t,
		runnerSQLStep{
			kind:     "query",
			contains: "UPDATE auth_outbox_events event",
			columns:  []string{"id", "payload"},
			rows:     [][]driver.Value{{"event-2", payload}},
		},
		runnerSQLStep{
			kind:     "exec",
			contains: "INSERT INTO account_privacy_profiles",
			affected: 1,
			check: func(args []driver.NamedValue) error {
				if args[1].Value != "Retry User" {
					return fmt.Errorf("unexpected retry profile args: %#v", args)
				}
				return nil
			},
		},
		runnerSQLStep{
			kind:     "query",
			contains: "SELECT f10_provision_trial",
			columns:  []string{"f10_provision_trial"},
			rows:     [][]driver.Value{{false}},
			check: func(args []driver.NamedValue) error {
				if args[0].Value != "workspace-existing" || args[1].Value != eventTime {
					return fmt.Errorf("unexpected retry trial args: %#v", args)
				}
				return nil
			},
		},
		runnerSQLStep{
			kind:     "exec",
			contains: "SET published_at = $2",
			affected: 1,
		},
	)
	previousFactory := newWorkspaceRuntimeService
	t.Cleanup(func() { newWorkspaceRuntimeService = previousFactory })
	newWorkspaceRuntimeService = func(*sql.DB, func() time.Time) (workspaceOnboardingRuntime, error) {
		return onboardingRuntimeStub{
			workspace: workspaces.Workspace{ID: "workspace-existing", Name: "Retry User's workspace"},
			created:   false,
		}, nil
	}

	processed, err := (&Runner{database: database, clock: func() time.Time { return now }}).
		processOnboardingEvent(context.Background())
	if err != nil {
		t.Fatalf("processOnboardingEvent() error = %v", err)
	}
	if !processed {
		t.Fatal("processOnboardingEvent() = false, want true")
	}
	assertRunnerSQLComplete(t, state)
}

type onboardingRuntimeStub struct {
	workspace workspaces.Workspace
	created   bool
	err       error
}

func (stub onboardingRuntimeStub) ConsumeOnboardingRequired(
	context.Context,
	workspaces.OnboardingRequiredEvent,
) (workspaces.Workspace, bool, error) {
	return stub.workspace, stub.created, stub.err
}
