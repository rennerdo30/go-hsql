package hsql

import (
	"database/sql"
	"fmt"
	"testing"
	"time"
)

func TestScalarTypes(t *testing.T) {
	db := openDB(t)
	c := ctx(t)

	if _, err := db.ExecContext(c, `CREATE TABLE types_t (
		i  INTEGER,
		bi BIGINT,
		si SMALLINT,
		ti TINYINT,
		d  DOUBLE,
		bo BOOLEAN,
		vc VARCHAR(50),
		ch CHAR(5),
		dec DECIMAL(12,3),
		bin VARBINARY(16)
	)`); err != nil {
		t.Fatalf("create: %v", err)
	}

	_, err := db.ExecContext(c,
		"INSERT INTO types_t VALUES (?,?,?,?,?,?,?,?,?,?)",
		int64(2147483647),
		int64(9223372036854775807),
		int64(32000),
		int64(120),
		3.14159,
		true,
		"hello, 世界",
		"abc",
		"12345.678",
		[]byte{0x00, 0x01, 0xfe, 0xff},
	)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	var (
		i   int64
		bi  int64
		si  int64
		ti  int64
		d   float64
		bo  bool
		vc  string
		ch  string
		dec string
		bin []byte
	)
	row := db.QueryRowContext(c, "SELECT i,bi,si,ti,d,bo,vc,ch,dec,bin FROM types_t")
	if err := row.Scan(&i, &bi, &si, &ti, &d, &bo, &vc, &ch, &dec, &bin); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if i != 2147483647 {
		t.Errorf("i=%d", i)
	}
	if bi != 9223372036854775807 {
		t.Errorf("bi=%d", bi)
	}
	if si != 32000 {
		t.Errorf("si=%d", si)
	}
	if ti != 120 {
		t.Errorf("ti=%d", ti)
	}
	if d != 3.14159 {
		t.Errorf("d=%v", d)
	}
	if !bo {
		t.Errorf("bo=%v", bo)
	}
	if vc != "hello, 世界" {
		t.Errorf("vc=%q (unicode round-trip failed)", vc)
	}
	if ch != "abc  " { // CHAR(5) is space-padded
		t.Errorf("ch=%q", ch)
	}
	if dec != "12345.678" {
		t.Errorf("dec=%q, want 12345.678", dec)
	}
	if fmt.Sprintf("%x", bin) != "0001feff" {
		t.Errorf("bin=%x, want 0001feff", bin)
	}
}

func TestTemporalTypes(t *testing.T) {
	db := openDB(t)
	c := ctx(t)

	if _, err := db.ExecContext(c, `CREATE TABLE temporal_t (
		dt DATE,
		ts TIMESTAMP
	)`); err != nil {
		t.Fatalf("create: %v", err)
	}

	if _, err := db.ExecContext(c,
		"INSERT INTO temporal_t VALUES (DATE '2023-11-15', TIMESTAMP '2023-11-15 14:30:45.123456')"); err != nil {
		t.Fatalf("insert: %v", err)
	}

	var dt, ts time.Time
	if err := db.QueryRowContext(c, "SELECT dt, ts FROM temporal_t").Scan(&dt, &ts); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if dt.Year() != 2023 || dt.Month() != time.November || dt.Day() != 15 {
		t.Errorf("date = %v, want 2023-11-15", dt)
	}
	if ts.Hour() != 14 || ts.Minute() != 30 || ts.Second() != 45 {
		t.Errorf("timestamp time-of-day = %v, want 14:30:45", ts)
	}
	if ts.Nanosecond() != 123456000 {
		t.Errorf("timestamp nanos = %d, want 123456000", ts.Nanosecond())
	}
}

func TestResultPaging(t *testing.T) {
	// Force a small fetch size so the server returns rows in multiple blocks,
	// exercising the REQUESTDATA paging path.
	srv := startTestServer(t)
	db, err := sql.Open("hsql", srv.dsn+"?fetchsize=10")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	c := ctx(t)

	if _, err := db.ExecContext(c, "CREATE TABLE big (n INTEGER)"); err != nil {
		t.Fatalf("create: %v", err)
	}
	stmt, err := db.PrepareContext(c, "INSERT INTO big VALUES (?)")
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	const total = 95
	for i := 1; i <= total; i++ {
		if _, err := stmt.ExecContext(c, i); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}
	stmt.Close()

	rows, err := db.QueryContext(c, "SELECT n FROM big ORDER BY n")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()
	count, sum := 0, 0
	for rows.Next() {
		var n int
		if err := rows.Scan(&n); err != nil {
			t.Fatalf("scan: %v", err)
		}
		count++
		sum += n
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows err: %v", err)
	}
	if count != total {
		t.Fatalf("row count = %d, want %d (paging lost rows)", count, total)
	}
	if want := total * (total + 1) / 2; sum != want {
		t.Fatalf("sum = %d, want %d", sum, want)
	}
}
