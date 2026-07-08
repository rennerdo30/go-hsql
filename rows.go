package hsql

import (
	"database/sql/driver"
	"io"

	"github.com/rennerdo30/go-hsql/internal/proto"
)

// rows implements driver.Rows over a result-set, fetching additional blocks via
// REQUESTDATA when the initial block does not contain every row.
type rows struct {
	conn *conn
	meta *proto.Metadata

	resultID int64
	total    int32   // total rows in the full result (RowSet.Size)
	block    [][]any // current in-memory block
	blockPos int     // index into block
	absPos   int32   // absolute row index consumed so far
	done     bool
	next     []*proto.Result
}

var (
	_ driver.Rows              = (*rows)(nil)
	_ driver.RowsNextResultSet = (*rows)(nil)
)

func newRows(c *conn, res *proto.Result) *rows {
	r := &rows{conn: c, meta: res.Meta}
	r.loadResult(res)
	for _, chained := range res.Chained {
		if isRowsResult(chained) {
			r.next = append(r.next, chained)
		}
	}
	return r
}

func (r *rows) loadResult(res *proto.Result) {
	r.meta = res.Meta
	r.resultID = 0
	r.total = 0
	r.block = nil
	r.blockPos = 0
	r.absPos = 0
	r.done = false
	if res.RowSet != nil {
		r.resultID = res.RowSet.ID
		r.total = res.RowSet.Size
		r.block = res.RowSet.Rows
	}
	if r.meta == nil {
		r.done = true
	}
}

// Columns returns the visible column labels.
func (r *rows) Columns() []string {
	if r.meta == nil {
		return nil
	}
	names := make([]string, len(r.meta.Columns))
	for i, c := range r.meta.Columns {
		names[i] = c.Label
	}
	return names
}

// Next reads the next row into dest.
func (r *rows) Next(dest []driver.Value) error {
	if r.done {
		return io.EOF
	}
	if r.blockPos >= len(r.block) {
		if err := r.fetchMore(); err != nil {
			return err
		}
		if r.done || r.blockPos >= len(r.block) {
			r.done = true
			return io.EOF
		}
	}
	row := r.block[r.blockPos]
	r.blockPos++
	r.absPos++
	n := len(dest)
	if len(r.meta.Columns) < n {
		n = len(r.meta.Columns)
	}
	for i := 0; i < n; i++ {
		v := row[i]
		if ref, ok := v.(proto.LobRef); ok {
			// Resolve the LOB payload on demand.
			resolved, err := r.conn.fetchLob(ref)
			if err != nil {
				return err
			}
			v = resolved
		}
		dest[i] = driver.Value(v)
	}
	return nil
}

// fetchMore requests the next block of rows when the current one is exhausted
// but the full result has more rows remaining.
func (r *rows) fetchMore() error {
	if r.absPos >= r.total {
		r.done = true
		return nil
	}
	req := proto.NewResult(proto.ModeRequestData)
	req.ID = r.resultID
	req.UpdateCount = r.absPos // resume at the next unread row
	req.FetchSize = int32(r.conn.cfg.FetchSize)
	res, err := r.conn.exec(req)
	if err != nil {
		return err
	}
	if res.RowSet == nil || len(res.RowSet.Rows) == 0 {
		r.done = true
		return nil
	}
	r.block = res.RowSet.Rows
	r.blockPos = 0
	if res.Meta != nil {
		r.meta = res.Meta
	}
	return nil
}

// Close closes the server-side cursor for this result set. The server only
// retains a cursor (in its resultMap) when rows remain undelivered; if every
// row has already been sent it keeps nothing, and issuing CLOSE_RESULT would
// dereference a nil map. So we close only when rows remain outstanding.
func (r *rows) Close() error {
	r.done = true
	r.next = nil
	return r.closeCurrent()
}

func (r *rows) closeCurrent() error {
	if r.conn == nil || r.conn.closed || r.resultID == 0 {
		return nil
	}
	if r.absPos >= r.total {
		return nil // fully consumed; no server-side cursor to close
	}
	req := proto.NewResult(proto.ModeCloseResult)
	req.ID = r.resultID
	_, err := r.conn.exec(req)
	return err
}

// HasNextResultSet reports whether the server sent another chained result set.
func (r *rows) HasNextResultSet() bool {
	return len(r.next) > 0
}

// NextResultSet advances to the next chained result set, if any.
func (r *rows) NextResultSet() error {
	if len(r.next) == 0 {
		return io.EOF
	}
	if err := r.closeCurrent(); err != nil {
		return err
	}
	next := r.next[0]
	r.next = r.next[1:]
	r.loadResult(next)
	return nil
}
