package hsql

import (
	"database/sql"
	"database/sql/driver"
	"reflect"
	"testing"
	"time"

	"github.com/rennerdo30/go-hsql/internal/proto"
)

func TestErrorString(t *testing.T) {
	e := &Error{Message: "bad", SQLState: "42S02", ErrorCode: -5501}
	got := e.Error()
	if got == "" || !contains(got, "bad") || !contains(got, "42S02") {
		t.Fatalf("Error() = %q", got)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestTypeNameMapping(t *testing.T) {
	cases := map[proto.TypeCode]string{
		proto.SQLInteger:     "INTEGER",
		proto.SQLVarchar:     "VARCHAR",
		proto.SQLDecimal:     "DECIMAL",
		proto.SQLBoolean:     "BOOLEAN",
		proto.SQLTimestamp:   "TIMESTAMP",
		proto.SQLTimestampTZ: "TIMESTAMP WITH TIME ZONE",
		proto.SQLBlob:        "BLOB",
		proto.SQLClob:        "CLOB",
		proto.SQLBinary:      "BINARY",
		proto.SQLBigint:      "BIGINT",
		proto.SQLDouble:      "DOUBLE",
		proto.SQLDate:        "DATE",
		proto.SQLTime:        "TIME",
	}
	for code, want := range cases {
		if got := typeName(code); got != want {
			t.Errorf("typeName(%d) = %q, want %q", code, got, want)
		}
	}
	if typeName(proto.TypeCode(-999)) != "OTHER" {
		t.Error("unknown type should map to OTHER")
	}
}

func TestScanTypeMapping(t *testing.T) {
	if scanType(proto.SQLInteger).Kind() != reflect.Int64 {
		t.Error("integer scan type")
	}
	if scanType(proto.SQLDouble).Kind() != reflect.Float64 {
		t.Error("double scan type")
	}
	if scanType(proto.SQLBoolean).Kind() != reflect.Bool {
		t.Error("bool scan type")
	}
	if scanType(proto.SQLVarchar).Kind() != reflect.String {
		t.Error("varchar scan type")
	}
	if scanType(proto.SQLVarbinary) != reflect.TypeOf([]byte(nil)) {
		t.Error("varbinary scan type")
	}
	if scanType(proto.SQLTimestamp) != reflect.TypeOf(time.Time{}) {
		t.Error("timestamp scan type")
	}
}

func TestIsolationSQLMapping(t *testing.T) {
	cases := map[sql.IsolationLevel]string{
		sql.LevelReadUncommitted: "READ UNCOMMITTED",
		sql.LevelReadCommitted:   "READ COMMITTED",
		sql.LevelRepeatableRead:  "REPEATABLE READ",
		sql.LevelSerializable:    "SERIALIZABLE",
	}
	for lvl, want := range cases {
		got, err := isolationSQL(lvl)
		if err != nil || got != want {
			t.Errorf("isolationSQL(%v) = %q, %v", lvl, got, err)
		}
	}
	if _, err := isolationSQL(sql.IsolationLevel(99)); err == nil {
		t.Error("expected error for unknown isolation level")
	}
}

// TestLegacyDriverInterfaces exercises the non-context Driver/Conn/Stmt methods
// that database/sql normally bypasses in favor of the *Context variants.
func TestLegacyDriverInterfaces(t *testing.T) {
	srv := startTestServer(t)

	// Driver.Open (not OpenConnector).
	dc, err := (&Driver{}).Open(srv.dsn)
	if err != nil {
		t.Fatalf("Driver.Open: %v", err)
	}
	defer dc.Close()
	c := dc.(*conn)

	// Legacy Conn.Prepare + Stmt.Exec/Query (no context).
	if _, err := c.exec(c.newExecDirect("CREATE TABLE lg (n INTEGER)")); err != nil {
		t.Fatalf("create: %v", err)
	}
	st, err := c.Prepare("INSERT INTO lg VALUES (?)")
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if _, err := st.Exec([]driver.Value{int64(5)}); err != nil {
		t.Fatalf("Stmt.Exec: %v", err)
	}
	_ = st.Close()

	q, err := c.Prepare("SELECT n FROM lg")
	if err != nil {
		t.Fatalf("Prepare select: %v", err)
	}
	rows, err := q.Query(nil)
	if err != nil {
		t.Fatalf("Stmt.Query: %v", err)
	}
	dest := make([]driver.Value, 1)
	if err := rows.Next(dest); err != nil {
		t.Fatalf("rows.Next: %v", err)
	}
	if dest[0] != int64(5) {
		t.Fatalf("got %v, want 5", dest[0])
	}
	_ = rows.Close()
	_ = q.Close()

	// Legacy Conn.Begin + Tx commit.
	tx, err := c.Begin()
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if _, err := c.exec(c.newExecDirect("INSERT INTO lg VALUES (9)")); err != nil {
		t.Fatalf("insert in tx: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
}
