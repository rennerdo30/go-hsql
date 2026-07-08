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
	return counts, nil
}
