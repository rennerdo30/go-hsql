package hsql

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
)

// tx implements driver.Tx. Transactions are driven with SQL session commands
// (SET AUTOCOMMIT / COMMIT / ROLLBACK), which HSQLDB accepts directly.
type tx struct {
	conn *conn
}

var _ driver.Tx = (*tx)(nil)

// Begin implements the legacy driver.Conn interface.
func (c *conn) Begin() (driver.Tx, error) {
	return c.BeginTx(context.Background(), driver.TxOptions{})
}

// BeginTx implements driver.ConnBeginTx.
func (c *conn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	if c.closed {
		return nil, driver.ErrBadConn
	}
	if !c.autocommit {
		return nil, fmt.Errorf("hsql: nested transactions are not supported")
	}
	if lvl := sql.IsolationLevel(opts.Isolation); lvl != sql.LevelDefault {
		iso, err := isolationSQL(lvl)
		if err != nil {
			return nil, err
		}
		if _, err := c.execCtx(ctx, c.newExecDirect("SET TRANSACTION ISOLATION LEVEL "+iso)); err != nil {
			return nil, err
		}
	}
	if opts.ReadOnly {
		if _, err := c.execCtx(ctx, c.newExecDirect("SET TRANSACTION READ ONLY")); err != nil {
			return nil, err
		}
	}
	if _, err := c.execCtx(ctx, c.newExecDirect("SET AUTOCOMMIT FALSE")); err != nil {
		return nil, err
	}
	c.autocommit = false
	return &tx{conn: c}, nil
}

// Commit commits the transaction and restores autocommit mode.
func (t *tx) Commit() error { return t.finish("COMMIT") }

// Rollback rolls back the transaction and restores autocommit mode.
func (t *tx) Rollback() error { return t.finish("ROLLBACK") }

func (t *tx) finish(verb string) error {
	c := t.conn
	if c.closed {
		return driver.ErrBadConn
	}
	if _, err := c.exec(c.newExecDirect(verb)); err != nil {
		// Even on error, try to restore autocommit so the pooled connection
		// is usable again.
		_, _ = c.exec(c.newExecDirect("SET AUTOCOMMIT TRUE"))
		c.autocommit = true
		return err
	}
	if _, err := c.exec(c.newExecDirect("SET AUTOCOMMIT TRUE")); err != nil {
		return err
	}
	c.autocommit = true
	return nil
}

// isolationSQL maps a sql.IsolationLevel to its HSQLDB SQL keyword.
func isolationSQL(lvl sql.IsolationLevel) (string, error) {
	switch lvl {
	case sql.LevelReadUncommitted:
		return "READ UNCOMMITTED", nil
	case sql.LevelReadCommitted:
		return "READ COMMITTED", nil
	case sql.LevelRepeatableRead:
		return "REPEATABLE READ", nil
	case sql.LevelSerializable:
		return "SERIALIZABLE", nil
	default:
		return "", fmt.Errorf("hsql: unsupported isolation level %d", lvl)
	}
}
