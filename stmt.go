package hsql

import (
	"context"
	"database/sql/driver"
	"fmt"

	"github.com/rennerdo30/go-hsql/internal/proto"
)

// PrepareContext implements driver.ConnPrepareContext.
func (c *conn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	if c.closed {
		return nil, driver.ErrBadConn
	}
	req := proto.NewResult(proto.ModePrepare)
	req.SQL = query
	req.StatementReturnType = proto.StatementReturnAny
	// Request generated keys so a prepared INSERT can report LastInsertId. The
	// server ignores this for statements that produce no keys.
	req.GenerateKeys = proto.ReturnGeneratedKeys
	res, err := c.execCtx(ctx, req)
	if err != nil {
		return nil, err
	}
	if res.Mode != proto.ModePrepareAck {
		return nil, fmt.Errorf("hsql: unexpected prepare response mode %d", res.Mode)
	}
	return &stmt{
		conn:      c,
		id:        res.StatementID,
		meta:      res.Meta,
		paramMeta: res.ParamMeta,
	}, nil
}

// stmt implements driver.Stmt (with context variants).
type stmt struct {
	conn      *conn
	id        int64
	meta      *proto.Metadata // result-set metadata
	paramMeta *proto.Metadata // parameter metadata
	closed    bool
}

var (
	_ driver.Stmt              = (*stmt)(nil)
	_ driver.StmtExecContext   = (*stmt)(nil)
	_ driver.StmtQueryContext  = (*stmt)(nil)
	_ driver.NamedValueChecker = (*stmt)(nil)
)

// NumInput returns the number of bound parameters.
func (s *stmt) NumInput() int {
	if s.paramMeta == nil {
		return 0
	}
	return int(s.paramMeta.ColumnCount)
}

// Close frees the prepared statement on the server.
func (s *stmt) Close() error {
	if s.closed || s.conn.closed {
		return nil
	}
	s.closed = true
	req := proto.NewResult(proto.ModeFreeStmt)
	req.StatementID = s.id
	_, err := s.conn.exec(req)
	return err
}

// Exec implements the legacy driver.Stmt interface.
func (s *stmt) Exec(args []driver.Value) (driver.Result, error) {
	return s.ExecContext(context.Background(), valuesToNamed(args))
}

// Query implements the legacy driver.Stmt interface.
func (s *stmt) Query(args []driver.Value) (driver.Rows, error) {
	return s.QueryContext(context.Background(), valuesToNamed(args))
}

// ExecContext implements driver.StmtExecContext.
func (s *stmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	res, err := s.execute(ctx, args)
	if err != nil {
		return nil, err
	}
	return resultFromResponse(res), nil
}

// QueryContext implements driver.StmtQueryContext.
func (s *stmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	res, err := s.execute(ctx, args)
	if err != nil {
		return nil, err
	}
	return s.conn.rowsFromResponse(res), nil
}

// CheckNamedValue lets database/sql pass driver-specific streaming LOB
// parameters through without converting them to the standard driver.Value set.
func (s *stmt) CheckNamedValue(nv *driver.NamedValue) error {
	return checkNamedValue(nv)
}

func checkNamedValue(nv *driver.NamedValue) error {
	switch nv.Value.(type) {
	case Blob, *Blob, Clob, *Clob:
		return nil
	default:
		return driver.ErrSkip
	}
}

// execute sends an EXECUTE request binding the given arguments.
func (s *stmt) execute(ctx context.Context, args []driver.NamedValue) (*proto.Result, error) {
	if s.conn.closed || s.conn.broken {
		return nil, driver.ErrBadConn
	}
	req := proto.NewResult(proto.ModeExecute)
	req.StatementID = s.id
	req.StatementReturnType = proto.StatementReturnAny
	req.RSProperties = proto.DefaultRSProperties
	req.FetchSize = int32(s.conn.cfg.FetchSize)
	req.ParamMeta = s.paramMeta
	req.ParamValues = namedToValues(args)
	return s.conn.execCtx(ctx, req)
}

// namedToValues flattens ordered NamedValue args into a positional slice.
func namedToValues(args []driver.NamedValue) []any {
	out := make([]any, len(args))
	for _, a := range args {
		idx := a.Ordinal - 1
		if idx >= 0 && idx < len(out) {
			out[idx] = a.Value
		}
	}
	return out
}

// valuesToNamed adapts positional values to NamedValue for the context path.
func valuesToNamed(args []driver.Value) []driver.NamedValue {
	out := make([]driver.NamedValue, len(args))
	for i, v := range args {
		out[i] = driver.NamedValue{Ordinal: i + 1, Value: v}
	}
	return out
}
