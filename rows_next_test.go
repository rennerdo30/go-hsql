package hsql

import (
	"database/sql/driver"
	"io"
	"testing"

	"github.com/rennerdo30/go-hsql/internal/proto"
)

func TestRowsNextResultSet(t *testing.T) {
	first := dataResult("a", int64(1))
	first.Chained = []*proto.Result{
		dataResult("b", int64(2)),
		dataResult("c", int64(3)),
	}
	r := newRows(nil, first)

	if cols := r.Columns(); len(cols) != 1 || cols[0] != "a" {
		t.Fatalf("first columns = %v", cols)
	}
	dest := make([]driver.Value, 1)
	if err := r.Next(dest); err != nil || dest[0] != int64(1) {
		t.Fatalf("first next = %v %v", dest, err)
	}
	if err := r.Next(dest); err != io.EOF {
		t.Fatalf("first EOF = %v", err)
	}
	if !r.HasNextResultSet() {
		t.Fatal("expected second result set")
	}
	if err := r.NextResultSet(); err != nil {
		t.Fatalf("next result set: %v", err)
	}
	if cols := r.Columns(); len(cols) != 1 || cols[0] != "b" {
		t.Fatalf("second columns = %v", cols)
	}
	if err := r.Next(dest); err != nil || dest[0] != int64(2) {
		t.Fatalf("second next = %v %v", dest, err)
	}
	if err := r.NextResultSet(); err != nil {
		t.Fatalf("third result set: %v", err)
	}
	if err := r.Next(dest); err != nil || dest[0] != int64(3) {
		t.Fatalf("third next = %v %v", dest, err)
	}
	if r.HasNextResultSet() {
		t.Fatal("unexpected fourth result set")
	}
	if err := r.NextResultSet(); err != io.EOF {
		t.Fatalf("final NextResultSet = %v", err)
	}
}

func dataResult(label string, v int64) *proto.Result {
	return &proto.Result{
		Mode: proto.ModeData,
		Meta: &proto.Metadata{
			ColumnCount:   1,
			ExtendedCount: 1,
			Types:         []proto.ColumnType{{Code: proto.SQLInteger}},
			Columns:       []proto.Column{{Type: proto.ColumnType{Code: proto.SQLInteger}, Label: label}},
		},
		RowSet: &proto.RowSet{
			Size: 1,
			Rows: [][]any{{v}},
		},
	}
}
