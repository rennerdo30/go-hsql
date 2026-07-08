package hsql

import (
	"context"
	"database/sql/driver"
	"fmt"

	"github.com/rennerdo30/go-hsql/internal/proto"
)

// Savepoint creates or replaces a transaction savepoint on this connection.
// It is intended for use through database/sql Conn.Raw.
func (c *conn) Savepoint(ctx context.Context, name string) error {
	if name == "" {
		return fmt.Errorf("hsql: savepoint name is empty")
	}
	if c.closed || c.broken {
		return driver.ErrBadConn
	}
	req := proto.NewResult(proto.ModeSetConnectAttr)
	req.ConnectAttr = proto.ConnectAttrSavepointName
	req.SavepointName = name
	_, err := c.execCtx(ctx, req)
	return err
}

// RollbackToSavepoint rolls back the current transaction to a savepoint.
func (c *conn) RollbackToSavepoint(ctx context.Context, name string) error {
	return c.savepointEnd(ctx, proto.TxSavepointNameRollback, name)
}

// ReleaseSavepoint releases a savepoint and all savepoints created after it.
func (c *conn) ReleaseSavepoint(ctx context.Context, name string) error {
	return c.savepointEnd(ctx, proto.TxSavepointNameRelease, name)
}

func (c *conn) savepointEnd(ctx context.Context, txType int32, name string) error {
	if name == "" {
		return fmt.Errorf("hsql: savepoint name is empty")
	}
	if c.closed || c.broken {
		return driver.ErrBadConn
	}
	req := proto.NewResult(proto.ModeEndTran)
	req.TxType = txType
	req.SavepointName = name
	_, err := c.execCtx(ctx, req)
	return err
}
