package emailruntime

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"testing"
	"time"

	email "github.com/apdsoftware/postqron/features/f14-email"
)

func TestSQLStoreEnqueueReturnsCreatedInsertResult(t *testing.T) {
	database := sql.OpenDB(emailStoreConnector{state: &emailStoreState{
		rowsAffected: 1,
		id:           "email-created",
		state:        email.StatePending,
	}})
	t.Cleanup(func() { _ = database.Close() })
	store := &sqlStore{database: database}

	result, err := store.Enqueue(context.Background(), testDelivery("idempotency-created"))
	if err != nil {
		t.Fatal(err)
	}
	if result.ID != "email-created" || !result.Created || result.State != email.StatePending {
		t.Fatalf("enqueue result = %+v", result)
	}
}

func TestSQLStoreEnqueueReturnsExistingRowOnIdempotencyConflict(t *testing.T) {
	database := sql.OpenDB(emailStoreConnector{state: &emailStoreState{
		rowsAffected: 0,
		id:           "email-existing",
		state:        email.StateAccepted,
	}})
	t.Cleanup(func() { _ = database.Close() })
	store := &sqlStore{database: database}

	result, err := store.Enqueue(context.Background(), testDelivery("idempotency-existing"))
	if err != nil {
		t.Fatal(err)
	}
	if result.ID != "email-existing" || result.Created || result.State != email.StateAccepted {
		t.Fatalf("enqueue result = %+v", result)
	}
}

func testDelivery(idempotencyKey string) email.Delivery {
	now := time.Unix(1_800_000_000, 0).UTC()
	return email.Delivery{
		Message: email.Message{
			ID:              "email-random",
			IdempotencyKey:  idempotencyKey,
			Channel:         email.ChannelTransactional,
			Template:        email.TemplateAccountVerification,
			TemplateVersion: "1.0.0",
			CreatedAt:       now,
			MaxAttempts:     5,
		},
		Rendered: email.RenderedMessage{
			Recipient: email.Recipient{
				ID:    "account-1",
				Email: "user@example.test",
			},
			Locale:    email.LocaleEnglish,
			Subject:   "Verify your account",
			Preheader: "Complete sign in",
			HTML:      "<a>verify</a>",
			Text:      "verify",
		},
		NextAttemptAt: now,
	}
}

type emailStoreState struct {
	rowsAffected int64
	id           string
	state        email.DeliveryState
}

type emailStoreConnector struct {
	state *emailStoreState
}

func (connector emailStoreConnector) Connect(context.Context) (driver.Conn, error) {
	return emailStoreConn{state: connector.state}, nil
}

func (connector emailStoreConnector) Driver() driver.Driver {
	return emailStoreDriver{state: connector.state}
}

type emailStoreDriver struct {
	state *emailStoreState
}

func (driverState emailStoreDriver) Open(string) (driver.Conn, error) {
	return emailStoreConn{state: driverState.state}, nil
}

type emailStoreConn struct {
	state *emailStoreState
}

func (emailStoreConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("prepare is not supported")
}

func (emailStoreConn) Close() error { return nil }

func (emailStoreConn) Begin() (driver.Tx, error) {
	return nil, errors.New("transactions are not supported")
}

func (connection emailStoreConn) ExecContext(
	context.Context,
	string,
	[]driver.NamedValue,
) (driver.Result, error) {
	return emailStoreResult{rowsAffected: connection.state.rowsAffected}, nil
}

func (connection emailStoreConn) QueryContext(
	context.Context,
	string,
	[]driver.NamedValue,
) (driver.Rows, error) {
	return &emailStoreRows{
		id:    connection.state.id,
		state: connection.state.state,
	}, nil
}

type emailStoreResult struct {
	rowsAffected int64
}

func (result emailStoreResult) LastInsertId() (int64, error) { return 0, nil }
func (result emailStoreResult) RowsAffected() (int64, error) {
	return result.rowsAffected, nil
}

type emailStoreRows struct {
	id    string
	state email.DeliveryState
	sent  bool
}

func (*emailStoreRows) Columns() []string {
	return []string{"id", "state"}
}

func (*emailStoreRows) Close() error { return nil }

func (rows *emailStoreRows) Next(values []driver.Value) error {
	if rows.sent {
		return io.EOF
	}
	rows.sent = true
	values[0] = rows.id
	values[1] = string(rows.state)
	return nil
}
