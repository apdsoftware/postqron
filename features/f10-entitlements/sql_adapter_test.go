package entitlements

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestSQLDatabaseQueryRowNormalizesNoRows(t *testing.T) {
	database := sql.OpenDB(f10NoRowsConnector{})
	t.Cleanup(func() { _ = database.Close() })

	row := sqlDatabase{DB: database}.QueryRow(
		context.Background(),
		"SELECT value",
	)
	var value string
	err := row.Scan(&value)

	if err != pgx.ErrNoRows {
		t.Fatalf("Scan() error = %v, want pgx.ErrNoRows", err)
	}
}

type f10NoRowsConnector struct{}

func (f10NoRowsConnector) Connect(context.Context) (driver.Conn, error) {
	return f10NoRowsConn{}, nil
}

func (f10NoRowsConnector) Driver() driver.Driver {
	return f10NoRowsDriver{}
}

type f10NoRowsDriver struct{}

func (f10NoRowsDriver) Open(string) (driver.Conn, error) {
	return f10NoRowsConn{}, nil
}

type f10NoRowsConn struct{}

func (f10NoRowsConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("prepare is not supported")
}

func (f10NoRowsConn) Close() error {
	return nil
}

func (f10NoRowsConn) Begin() (driver.Tx, error) {
	return nil, errors.New("transactions are not supported")
}

func (f10NoRowsConn) QueryContext(
	context.Context,
	string,
	[]driver.NamedValue,
) (driver.Rows, error) {
	return f10EmptyRows{}, nil
}

type f10EmptyRows struct{}

func (f10EmptyRows) Columns() []string {
	return []string{"value"}
}

func (f10EmptyRows) Close() error {
	return nil
}

func (f10EmptyRows) Next([]driver.Value) error {
	return io.EOF
}
