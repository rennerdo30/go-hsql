package hsql

import (
	"context"
	"database/sql/driver"
	"fmt"
	"strings"

	"github.com/rennerdo30/go-hsql/internal/proto"
)

// Updatable result sets (JDBC CONCUR_UPDATABLE parity) speak the UPDATE_RESULT
// sub-protocol: the query runs as a scrollable, updatable server-side cursor
// whose rows can be updated, deleted or appended through the cursor itself.
// database/sql has no surface for this, so it is exposed driver-specifically
// through Conn.Raw:
//
//	conn, _ := db.Conn(ctx)
//	conn.Raw(func(dc any) error {
//		q := dc.(interface {
//			QueryUpdatable(ctx context.Context, query string, args ...any) (*hsql.UpdatableRows, error)
//		})
//		rows, err := q.QueryUpdatable(ctx, "SELECT id, name FROM t")
//		...
//	})

// errRowsClosed is returned when an UpdatableRows is used after Close.
var errRowsClosed = fmt.Errorf("hsql: updatable rows are closed")

// errNoCurrentRow is returned by row operations before Next selected a row.
var errNoCurrentRow = fmt.Errorf("hsql: no current row; call Next first")

// QueryUpdatable executes query as an updatable, scrollable cursor and returns
// the full result for iteration and in-place modification. The entire result
// is materialized client-side (like the reference JDBC client's scrollable
// navigator). Intended for use through database/sql Conn.Raw.
func (c *conn) QueryUpdatable(ctx context.Context, query string, args ...any) (*UpdatableRows, error) {
	if c.closed || c.broken {
		return nil, driver.ErrBadConn
	}
	props := proto.RSUpdatable | proto.RSScrollable | proto.RSHoldable

	prepReq := proto.NewResult(proto.ModePrepare)
	prepReq.SQL = query
	prepReq.StatementReturnType = proto.StatementReturnResult
	prepReq.RSProperties = props
	prepReq.GenerateKeys = proto.ReturnNoGeneratedKeys
	prep, err := c.execCtx(ctx, prepReq)
	if err != nil {
		return nil, err
	}
	if prep.Mode != proto.ModePrepareAck {
		return nil, fmt.Errorf("hsql: unexpected prepare response mode %d", prep.Mode)
	}
	st := &stmt{conn: c, id: prep.StatementID, meta: prep.Meta, paramMeta: prep.ParamMeta}
	if prep.RSProperties&proto.RSUpdatable == 0 {
		_ = st.Close()
		return nil, fmt.Errorf("hsql: query is not updatable: %s", query)
	}

	execReq := proto.NewResult(proto.ModeExecute)
	execReq.StatementID = st.id
	execReq.StatementReturnType = proto.StatementReturnResult
	execReq.RSProperties = props
	// Fetch everything at once: cursor modifications are tracked against the
	// client-side copy, so paging blocks through REQUESTDATA would desync.
	execReq.FetchSize = 0
	execReq.ParamMeta = st.paramMeta
	execReq.ParamValues = args
	res, err := c.execCtx(ctx, execReq)
	if err != nil {
		_ = st.Close()
		return nil, err
	}
	if !isRowsResult(res) {
		_ = st.Close()
		return nil, fmt.Errorf("hsql: unexpected updatable query response mode %d", res.Mode)
	}
	if res.Meta.ExtendedCount <= res.Meta.ColumnCount {
		_ = st.Close()
		return nil, fmt.Errorf("hsql: server returned no updatable cursor metadata for: %s", query)
	}
	// The server numbers the result's rows once at execution time and keeps
	// that numbering for the cursor's lifetime; remember it per cached row so
	// client-side deletes (which compact this copy) don't shift it.
	serverNums := make([]int64, len(res.RowSet.Rows))
	for i := range serverNums {
		serverNums[i] = int64(i)
	}
	return &UpdatableRows{
		conn:       c,
		stmt:       st,
		meta:       res.Meta,
		resultID:   res.RowSet.ID,
		rows:       res.RowSet.Rows,
		serverNums: serverNums,
		pos:        -1,
	}, nil
}

// UpdatableRows is a fully materialized, updatable result set. Iterate with
// Next/Scan; modify the current row with UpdateRow/DeleteRow or append with
// InsertRow. Close releases the server-side cursor and its statement.
//
// UpdatableRows is driver-specific API (obtained via QueryUpdatable through
// Conn.Raw) and must not be used concurrently.
type UpdatableRows struct {
	conn       *conn
	stmt       *stmt
	meta       *proto.Metadata
	resultID   int64
	rows       [][]any
	serverNums []int64 // server-side row number of each cached row
	pos        int
	closed     bool
}

// Columns returns the visible column labels.
func (u *UpdatableRows) Columns() []string {
	names := make([]string, len(u.meta.Columns))
	for i, col := range u.meta.Columns {
		names[i] = col.Label
	}
	return names
}

// Len returns the number of rows currently in the client-side result.
func (u *UpdatableRows) Len() int { return len(u.rows) }

// Next advances to the next row, reporting whether one exists.
func (u *UpdatableRows) Next() bool {
	if u.closed || u.pos+1 >= len(u.rows) {
		return false
	}
	u.pos++
	return true
}

// Scan copies the current row's visible columns into dest pointers, resolving
// LOB and ARRAY values like the standard rows path.
func (u *UpdatableRows) Scan(dest ...any) error {
	row, err := u.current()
	if err != nil {
		return err
	}
	n := len(dest)
	if n > len(u.meta.Columns) {
		n = len(u.meta.Columns)
	}
	for i := 0; i < n; i++ {
		v, err := u.conn.resolveValue(row[i])
		if err != nil {
			return err
		}
		if err := assignOutValue(dest[i], v); err != nil {
			return fmt.Errorf("hsql: column %d: %w", i, err)
		}
	}
	return nil
}

// UpdateRow updates columns of the current row through the cursor. Keys of
// updates are column labels (case-insensitive); values are the new column
// values. Columns not named keep their value.
func (u *UpdatableRows) UpdateRow(ctx context.Context, updates map[string]any) error {
	row, err := u.current()
	if err != nil {
		return err
	}
	if len(updates) == 0 {
		return nil
	}
	visible := int(u.meta.ColumnCount)
	ext := len(u.meta.Types)

	// Un-updated visible columns are marked SQL_ALL_TYPES so the server can
	// tell "not updated" from "set to NULL" (JDBCResultSet.performUpdate).
	types := make([]proto.ColumnType, ext)
	copy(types, u.meta.Types)
	values := make([]any, ext)
	set := make([]bool, visible)
	for name, v := range updates {
		idx, err := u.columnIndex(name)
		if err != nil {
			return err
		}
		values[idx] = v
		set[idx] = true
	}
	for i := 0; i < visible; i++ {
		if !set[i] {
			types[i] = proto.ColumnType{Code: proto.SQLAllTypes}
		}
	}
	u.fillRowIdentity(values, row)

	res, err := u.sendCursorAction(ctx, proto.ActionUpdateCursor, types, values)
	if err != nil {
		return err
	}
	u.mergeUpdatedRows(res)
	return nil
}

// DeleteRow deletes the current row through the cursor and removes it from the
// client-side result.
func (u *UpdatableRows) DeleteRow(ctx context.Context) error {
	row, err := u.current()
	if err != nil {
		return err
	}
	ext := len(u.meta.Types)
	values := make([]any, ext)
	u.fillRowIdentity(values, row)

	if _, err := u.sendCursorAction(ctx, proto.ActionDeleteCursor, u.meta.Types, values); err != nil {
		return err
	}
	u.rows = append(u.rows[:u.pos], u.rows[u.pos+1:]...)
	u.serverNums = append(u.serverNums[:u.pos], u.serverNums[u.pos+1:]...)
	u.pos--
	return nil
}

// InsertRow appends a new row through the cursor. Keys of values are column
// labels (case-insensitive); every non-identity column must be present.
func (u *UpdatableRows) InsertRow(ctx context.Context, values map[string]any) error {
	if u.closed {
		return errRowsClosed
	}
	ext := len(u.meta.Types)
	row := make([]any, ext)
	for name, v := range values {
		idx, err := u.columnIndex(name)
		if err != nil {
			return err
		}
		row[idx] = v
	}
	_, err := u.sendCursorAction(ctx, proto.ActionInsertCursor, u.meta.Types, row)
	return err
}

// Close releases the server-side cursor and frees the prepared statement.
func (u *UpdatableRows) Close() error {
	if u.closed {
		return nil
	}
	u.closed = true
	var firstErr error
	if !u.conn.closed && u.resultID != 0 {
		req := proto.NewResult(proto.ModeCloseResult)
		req.ID = u.resultID
		if _, err := u.conn.exec(req); err != nil {
			firstErr = err
		}
	}
	if err := u.stmt.Close(); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

func (u *UpdatableRows) current() ([]any, error) {
	if u.closed {
		return nil, errRowsClosed
	}
	if u.pos < 0 || u.pos >= len(u.rows) {
		return nil, errNoCurrentRow
	}
	return u.rows[u.pos], nil
}

func (u *UpdatableRows) columnIndex(name string) (int, error) {
	for i, col := range u.meta.Columns {
		if strings.EqualFold(col.Label, name) || strings.EqualFold(col.Name, name) {
			return i, nil
		}
	}
	return 0, fmt.Errorf("hsql: no such column %q", name)
}

// fillRowIdentity copies the cursor identity system columns (row id and the
// client row number) from the current row into an outgoing extended row.
func (u *UpdatableRows) fillRowIdentity(values, row []any) {
	visible := int(u.meta.ColumnCount)
	if idIdx := visible + proto.SysOffsetRowID; idIdx < len(row) && idIdx < len(values) {
		values[idIdx] = row[idIdx]
	}
	if numIdx := visible + proto.SysOffsetRowNum; numIdx < len(values) {
		values[numIdx] = u.serverNums[u.pos]
	}
}

// sendCursorAction issues one UPDATE_RESULT request against the cursor.
func (u *UpdatableRows) sendCursorAction(ctx context.Context, action int32, types []proto.ColumnType, values []any) (*proto.Result, error) {
	req := proto.NewResult(proto.ModeUpdateResult)
	req.ID = u.resultID
	req.ActionType = action
	req.Meta = &proto.Metadata{
		MetaType:    proto.MetaUpdateResult,
		ColumnCount: int32(len(types)),
		Types:       types,
	}
	req.BatchRows = [][]any{values}
	return u.conn.execCtx(ctx, req)
}

// mergeUpdatedRows folds the server's post-update row images back into the
// client-side copy (JDBCResultSet.performUpdate parity). The echoed row number
// refers to the server's original numbering, so it is matched against each
// cached row's remembered server number rather than its (delete-shifted) index.
func (u *UpdatableRows) mergeUpdatedRows(res *proto.Result) {
	if res == nil || res.RowSet == nil {
		return
	}
	numIdx := int(u.meta.ColumnCount) + proto.SysOffsetRowNum
	for _, updated := range res.RowSet.Rows {
		if numIdx >= len(updated) {
			continue
		}
		n, ok := updated[numIdx].(int64)
		if !ok {
			continue
		}
		for i, sn := range u.serverNums {
			if sn == n {
				u.rows[i] = updated
				break
			}
		}
	}
}
