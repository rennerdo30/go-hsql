package hsql

import (
	"context"
	"database/sql/driver"

	"github.com/rennerdo30/go-hsql/internal/proto"
)

// PrepareCommit performs the first phase of a two-phase commit (the
// PREPARECOMMIT message), matching the reference Java client's
// SessionInterface.prepareCommit. After a successful PrepareCommit the
// transaction is durable and must be finished with a normal Commit (or
// Rollback). Intended for use through database/sql Conn.Raw.
func (c *conn) PrepareCommit(ctx context.Context) error {
	if c.closed || c.broken {
		return driver.ErrBadConn
	}
	req := proto.NewResult(proto.ModeEndTran)
	req.TxType = proto.TxPrepareCommit
	_, err := c.execCtx(ctx, req)
	return err
}
