package hsql

import (
	"bytes"
	"context"
	"database/sql"
	"strings"
	"testing"
)

// TestColumnTypesWide exercises the column-type introspection paths across a
// wide range of SQL types against the real server.
func TestColumnTypesWide(t *testing.T) {
	db := openDB(t)
	c := ctx(t)
	if _, err := db.ExecContext(c, `CREATE TABLE wide (
		i INTEGER NOT NULL, bi BIGINT, si SMALLINT, ti TINYINT,
		r REAL, dp DOUBLE, dec DECIMAL(12,4), num NUMERIC(8,2),
		b BOOLEAN, vc VARCHAR(30), ch CHAR(4),
		dt DATE, tm TIME, ts TIMESTAMP,
		bin VARBINARY(8), blb BLOB, clb CLOB
	)`); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := db.ExecContext(c, "INSERT INTO wide (i) VALUES (1)"); err != nil {
		t.Fatalf("insert: %v", err)
	}
	rows, err := db.QueryContext(c, "SELECT * FROM wide")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()
	cts, err := rows.ColumnTypes()
	if err != nil {
		t.Fatalf("column types: %v", err)
	}
	want := map[string]string{
		"I": "INTEGER", "BI": "BIGINT", "SI": "SMALLINT", "TI": "TINYINT",
		// HSQLDB normalizes REAL to DOUBLE, so column R reports DOUBLE.
		"DP": "DOUBLE", "DEC": "DECIMAL", "NUM": "NUMERIC",
		"B": "BOOLEAN", "VC": "VARCHAR", "CH": "CHAR",
		"DT": "DATE", "TM": "TIME", "TS": "TIMESTAMP",
		"BIN": "VARBINARY", "BLB": "BLOB", "CLB": "CLOB",
	}
	seen := 0
	for _, ct := range cts {
		if exp, ok := want[ct.Name()]; ok {
			if ct.DatabaseTypeName() != exp {
				t.Errorf("%s: db type = %q, want %q", ct.Name(), ct.DatabaseTypeName(), exp)
			}
			seen++
		}
		_ = ct.ScanType()               // exercise scanType mapping
		_, _ = ct.Nullable()            // exercise nullability
		_, _ = ct.Length()              // exercise length
		_, _, _ = ct.DecimalSize()      // exercise precision/scale
	}
	if seen != len(want) {
		t.Fatalf("matched %d columns, want %d", seen, len(want))
	}
	// The NOT NULL column reports non-nullable; a plain column reports nullable.
	if nullable, ok := cts[0].Nullable(); ok && nullable {
		t.Errorf("column I should be NOT NULL")
	}
}

// TestLobStreamingKnownLength covers the known-length LOB create path (distinct
// from the unknown-length streaming path).
func TestLobStreamingKnownLength(t *testing.T) {
	db := openDB(t)
	c := ctx(t)
	if _, err := db.ExecContext(c, "CREATE TABLE lk (id INTEGER, b BLOB, t CLOB)"); err != nil {
		t.Fatalf("create: %v", err)
	}
	bin := bytes.Repeat([]byte{0xA5, 0x5A}, 4096)
	txt := strings.Repeat("stream ", 2000)
	if _, err := db.ExecContext(c, "INSERT INTO lk VALUES (?, ?, ?)",
		1, NewBlob(bytes.NewReader(bin), int64(len(bin))), NewClob(strings.NewReader(txt), int64(len(txt)))); err != nil {
		t.Fatalf("insert with known-length LOBs: %v", err)
	}
	var gotB []byte
	var gotT string
	if err := db.QueryRowContext(c, "SELECT b, t FROM lk WHERE id = 1").Scan(&gotB, &gotT); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if !bytes.Equal(gotB, bin) {
		t.Fatalf("BLOB mismatch: got %d bytes want %d", len(gotB), len(bin))
	}
	if gotT != txt {
		t.Fatalf("CLOB mismatch: got %d chars want %d", len(gotT), len(txt))
	}
}

// TestArrayScanFloatBool covers the float and bool element paths of ScanArray
// against real server ARRAY results.
func TestArrayScanFloatBool(t *testing.T) {
	db := openDB(t)
	c := ctx(t)
	if _, err := db.ExecContext(c, "CREATE TABLE av (id INTEGER, fs DOUBLE ARRAY, bs BOOLEAN ARRAY)"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := db.ExecContext(c, "INSERT INTO av VALUES (1, ARRAY[1.5,2.5,3.5], ARRAY[TRUE,FALSE,TRUE])"); err != nil {
		t.Fatalf("insert: %v", err)
	}
	var fs []float64
	var bs []bool
	if err := db.QueryRowContext(c, "SELECT fs, bs FROM av WHERE id = 1").Scan(ScanArray(&fs), ScanArray(&bs)); err != nil {
		t.Fatalf("scan arrays: %v", err)
	}
	if len(fs) != 3 || fs[1] != 2.5 {
		t.Fatalf("fs = %v", fs)
	}
	if len(bs) != 3 || bs[0] != true || bs[1] != false {
		t.Fatalf("bs = %v", bs)
	}
}

// TestCallScalar exercises the CALL response path.
func TestCallScalar(t *testing.T) {
	db := openDB(t)
	c := ctx(t)
	var n int
	if err := db.QueryRowContext(c, "CALL 6 * 7").Scan(&n); err != nil {
		t.Fatalf("CALL: %v", err)
	}
	if n != 42 {
		t.Fatalf("CALL result = %d, want 42", n)
	}
}

// TestIsolationLevelsRoundTrip exercises each supported isolation level against
// the real server.
func TestIsolationLevelsRoundTrip(t *testing.T) {
	db := openDB(t)
	c := ctx(t)
	if _, err := db.ExecContext(c, "CREATE TABLE il (n INTEGER)"); err != nil {
		t.Fatalf("create: %v", err)
	}
	// Every supported isolation level must at least begin, read, and commit.
	// (HSQLDB forces READ UNCOMMITTED transactions to be read-only, so we read
	// rather than write here.)
	levels := []sql.IsolationLevel{
		sql.LevelReadUncommitted, sql.LevelReadCommitted,
		sql.LevelRepeatableRead, sql.LevelSerializable,
	}
	for _, lvl := range levels {
		tx, err := db.BeginTx(c, &sql.TxOptions{Isolation: lvl})
		if err != nil {
			t.Fatalf("begin %v: %v", lvl, err)
		}
		var n int
		if err := tx.QueryRowContext(c, "SELECT COUNT(*) FROM il").Scan(&n); err != nil {
			t.Fatalf("read %v: %v", lvl, err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatalf("commit %v: %v", lvl, err)
		}
	}
}

// TestExecOnSelectAndQueryOnDDL covers the cross paths: Exec of a query and
// Query of a non-result statement.
func TestExecOnSelectAndQueryOnDDL(t *testing.T) {
	db := openDB(t)
	c := ctx(t)
	// Query on a DDL/DML statement returns an empty, column-less result.
	rows, err := db.QueryContext(c, "CREATE TABLE xq (n INTEGER)")
	if err != nil {
		t.Fatalf("query on DDL: %v", err)
	}
	cols, _ := rows.Columns()
	if len(cols) != 0 {
		t.Errorf("expected no columns for DDL query, got %v", cols)
	}
	rows.Close()

	// Exec of a SELECT succeeds (result discarded).
	if _, err := db.ExecContext(c, "SELECT * FROM xq"); err != nil {
		t.Fatalf("exec on select: %v", err)
	}
}

// TestConnectorDriver covers connector.Driver().
func TestConnectorDriver(t *testing.T) {
	d := &Driver{}
	connr, err := d.OpenConnector("hsql://SA@localhost:9001/x")
	if err != nil {
		t.Fatalf("open connector: %v", err)
	}
	if connr.Driver() == nil {
		t.Fatal("connector.Driver() returned nil")
	}
	_ = context.Background
}
