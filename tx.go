package hsql

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"

	"github.com/rennerdo30/go-hsql/internal/proto"
)

// tx implements driver.Tx. Transactions use the same binary protocol messages as
// the reference HSQLDB Java client: SETSESSIONATTR to toggle autocommit /
// read-only / isolation, and ENDTRAN to commit or roll back.
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
	if c.closed || c.broken {
		return nil, driver.ErrBadConn
	}
	if !c.autocommit {
		return nil, fmt.Errorf("hsql: nested transactions are not supported")
	}
	// Isolation and read-only are transaction-scoped via SET TRANSACTION so they
	// reset automatically at transaction end, matching database/sql's
	// per-transaction TxOptions (the binary INFO_ISOLATION / INFO_READONLY
	// attributes are connection-scoped and would leak into later transactions).
	if lvl := sql.IsolationLevel(opts.Isolation); lvl != sql.LevelDefault {
		iso, err := isolationSQL(lvl)
		if err != nil {
			return nil, err
		}
		if _, err := c.execCtx(ctx, c.newExecDirect("SET TRANSACTION ISOLATION LEVEL "+iso)); err != nil {
			return nil, err
		}
		c.txAlteredSession = true
	}
	if opts.ReadOnly {
		if _, err := c.execCtx(ctx, c.newExecDirect("SET TRANSACTION READ ONLY")); err != nil {
			return nil, err
		}
		c.txAlteredSession = true
	}
	// Autocommit and commit/rollback use the binary protocol, matching the Java
	// client's hot path.
	if err := c.setSessionAttrBool(ctx, proto.AttrAutocommit, false); err != nil {
		return nil, err
	}
	c.autocommit = false
	return &tx{conn: c}, nil
}

// Commit commits the transaction and restores autocommit mode.
func (t *tx) Commit() error { return t.finish(proto.TxCommit) }

// Rollback rolls back the transaction and restores autocommit mode.
func (t *tx) Rollback() error { return t.finish(proto.TxRollback) }

func (t *tx) finish(txType int32) error {
	c := t.conn
	if c.closed || c.broken {
		return driver.ErrBadConn
	}
	if err := c.endTran(txType); err != nil {
		// Even on error, try to restore autocommit so the pooled connection is
		// usable again.
		_ = c.setSessionAttrBool(context.Background(), proto.AttrAutocommit, true)
		c.autocommit = true
		c.resetSessionCharacteristics()
		return err
	}
	if err := c.setSessionAttrBool(context.Background(), proto.AttrAutocommit, true); err != nil {
		return err
	}
	c.autocommit = true
	c.resetSessionCharacteristics()
	return nil
}

// resetSessionCharacteristics restores the connection's default isolation and
// read-write mode after a transaction that changed them. HSQLDB applies some
// SET TRANSACTION settings at the session level, so without this a pooled
// connection could stay read-only or at the wrong isolation for the next user.
//
// READ UNCOMMITTED sets a sticky internal read-only-isolation flag that is only
// cleared when the default isolation actually *changes* to a non-READ-UNCOMMITTED
// level (Session.setIsolationDefault early-returns when the level is unchanged).
// So we toggle the default through SERIALIZABLE and back to READ COMMITTED to
// force the clear, then reset the explicit read-only flag. If any step fails the
// connection may still be read-only, so mark it broken to drop it from the pool.
func (c *conn) resetSessionCharacteristics() {
	if !c.txAlteredSession || c.closed || c.broken {
		return
	}
	c.txAlteredSession = false
	bg := context.Background()
	for _, err := range []error{
		c.setSessionAttrInt(bg, proto.AttrIsolation, proto.TxSerializable),
		c.setSessionAttrInt(bg, proto.AttrIsolation, proto.TxReadCommitted),
		c.setSessionAttrBool(bg, proto.AttrConnectionReadonly, false),
	} {
		if err != nil {
			c.broken = true
			return
		}
	}
}

// setSessionAttrInt sets an integer session attribute via SETSESSIONATTR.
func (c *conn) setSessionAttrInt(ctx context.Context, attrID, v int32) error {
	_, err := c.execCtx(ctx, proto.NewSessionAttrRequest(attrID, &v, nil, nil))
	return err
}

// endTran sends an ENDTRAN request (commit/rollback).
func (c *conn) endTran(txType int32) error {
	req := proto.NewResult(proto.ModeEndTran)
	req.TxType = txType
	_, err := c.exec(req)
	return err
}

// setSessionAttrBool sets a boolean session attribute via SETSESSIONATTR.
func (c *conn) setSessionAttrBool(ctx context.Context, attrID int32, v bool) error {
	_, err := c.execCtx(ctx, proto.NewSessionAttrRequest(attrID, nil, &v, nil))
	return err
}

// isolationSQL maps a sql.IsolationLevel to its HSQLDB SQL keyword for a
// transaction-scoped SET TRANSACTION ISOLATION LEVEL statement.
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
