package hsql

import (
	"context"
	"database/sql/driver"

	"github.com/rennerdo30/go-hsql/internal/proto"
)

// ExecBatch executes a batch of direct SQL update statements using HSQLDB's
// native BATCHEXECDIRECT protocol and returns one update count per statement.
// It is intended for use through database/sql Conn.Raw.
func (c *conn) ExecBatch(ctx context.Context, sqls []string) ([]int64, error) {
	if c.closed || c.broken {
		return nil, driver.ErrBadConn
	}
	req := proto.NewResult(proto.ModeBatchExecDirect)
	req.Meta = &proto.Metadata{
		MetaType:    proto.MetaSimpleResult,
		ColumnCount: 1,
		Types:       []proto.ColumnType{{Code: proto.SQLVarchar}},
	}
	req.BatchRows = make([][]any, len(sqls))
	for i, sql := range sqls {
		req.BatchRows[i] = []any{sql}
	}
	res, err := c.execCtx(ctx, req)
	if err != nil {
		return nil, err
	}
	return batchCounts(res), nil
}

// ExecPreparedBatch prepares query once and executes it for each row of
// parameters using HSQLDB's native BATCHEXECUTE protocol, returning one update
// count per row. Intended for use through database/sql Conn.Raw. Each element of
// argRows holds the positional parameter values for one execution.
func (c *conn) ExecPreparedBatch(ctx context.Context, query string, argRows [][]any) ([]int64, error) {
	if c.closed || c.broken {
		return nil, driver.ErrBadConn
	}
	st, err := c.PrepareContext(ctx, query)
	if err != nil {
		return nil, err
	}
	s := st.(*stmt)
	defer s.Close()

	req := proto.NewResult(proto.ModeBatchExecute)
	req.StatementID = s.id
	// BATCHEXECUTE carries SIMPLE metadata (parameter type codes only), matching
	// Result.newPreparedExecuteRequest in the Java client.
	req.Meta = &proto.Metadata{
		MetaType:    proto.MetaSimpleResult,
		ColumnCount: s.paramMeta.ColumnCount,
		Types:       s.paramMeta.Types,
	}
	req.BatchRows = argRows
	res, err := c.execCtx(ctx, req)
	if err != nil {
		return nil, err
	}
	return batchCounts(res), nil
}

// batchCounts extracts per-statement update counts from a BATCHEXECRESPONSE.
func batchCounts(res *proto.Result) []int64 {
	counts := make([]int64, 0)
	if res.RowSet != nil {
		for _, row := range res.RowSet.Rows {
			if len(row) == 0 {
				continue
			}
			switch v := row[0].(type) {
			case int64:
				counts = append(counts, v)
			case int:
				counts = append(counts, int64(v))
			}
		}
	}
	return counts
}
