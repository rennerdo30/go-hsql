package hsql

import (
	"context"
	"database/sql/driver"

	"github.com/rennerdo30/go-hsql/internal/proto"
)

// ExecContext implements driver.ExecerContext for statements without parameters.
// When arguments are present it returns driver.ErrSkip so database/sql falls
// back to the prepared-statement path.
func (c *conn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if len(args) > 0 {
		return nil, driver.ErrSkip
	}
	if c.closed || c.broken {
		return nil, driver.ErrBadConn
	}
	res, err := c.execCtx(ctx, c.newExecDirect(query))
	if err != nil {
		return nil, err
	}
	return resultFromResponse(res), nil
}

// QueryContext implements driver.QueryerContext for statements without
// parameters. Returns driver.ErrSkip when arguments are present.
func (c *conn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if len(args) > 0 {
		return nil, driver.ErrSkip
	}
	if c.closed || c.broken {
		return nil, driver.ErrBadConn
	}
	res, err := c.execCtx(ctx, c.newExecDirect(query))
	if err != nil {
		return nil, err
	}
	return c.rowsFromResponse(res), nil
}

// newExecDirect builds an EXECDIRECT request that may return either a result set
// or an update count, letting the server decide.
func (c *conn) newExecDirect(query string) *proto.Result {
	req := proto.NewResult(proto.ModeExecDirect)
	req.SQL = query
	req.StatementReturnType = proto.StatementReturnAny
	req.GenerateKeys = proto.ReturnNoGeneratedKeys
	req.FetchSize = int32(c.cfg.FetchSize)
	return req
}

// resultFromResponse builds a driver.Result from an EXEC/EXECDIRECT response.
func resultFromResponse(res *proto.Result) driver.Result {
	switch res.Mode {
	case proto.ModeUpdateCount:
		return &execResult{rowsAffected: int64(res.UpdateCount)}
	default:
		// A DATA or other response to an Exec: no update count available.
		return &execResult{rowsAffected: 0}
	}
}

// rowsFromResponse builds a driver.Rows from an EXEC/EXECDIRECT response. A
// non-result response (e.g. an update count) yields an empty, column-less Rows.
func (c *conn) rowsFromResponse(res *proto.Result) driver.Rows {
	if res.Mode == proto.ModeData || res.Mode == proto.ModeDataHead || res.Mode == proto.ModeGenerated {
		return newRows(c, res)
	}
	return &rows{conn: c, done: true}
}
