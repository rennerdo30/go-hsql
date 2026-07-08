package hsql

import (
	"database/sql"
	"testing"
)

func TestColumnTypes(t *testing.T) {
	db := openDB(t)
	c := ctx(t)
	if _, err := db.ExecContext(c, "CREATE TABLE ct (a INTEGER NOT NULL, b VARCHAR(30), d DOUBLE)"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := db.ExecContext(c, "INSERT INTO ct VALUES (1, 'x', 2.0)"); err != nil {
		t.Fatalf("insert: %v", err)
	}
	rows, err := db.QueryContext(c, "SELECT a, b, d FROM ct")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()

	cts, err := rows.ColumnTypes()
	if err != nil {
		t.Fatalf("column types: %v", err)
	}
	if len(cts) != 3 {
		t.Fatalf("got %d column types", len(cts))
	}
	if cts[0].DatabaseTypeName() != "INTEGER" {
		t.Errorf("col0 db type = %q", cts[0].DatabaseTypeName())
	}
	if cts[1].DatabaseTypeName() != "VARCHAR" {
		t.Errorf("col1 db type = %q", cts[1].DatabaseTypeName())
	}
	if st := cts[2].ScanType(); st == nil || st.Kind().String() != "float64" {
		t.Errorf("col2 scan type = %v", st)
	}
	// Nullability: column a is NOT NULL, b is nullable.
	if nullable, ok := cts[0].Nullable(); ok && nullable {
		t.Errorf("col a should be NOT NULL")
	}
	if nullable, ok := cts[1].Nullable(); ok && !nullable {
		t.Errorf("col b should be nullable")
	}
}

func TestTransactionIsolationAndReadOnly(t *testing.T) {
	db := openDB(t)
	c := ctx(t)
	if _, err := db.ExecContext(c, "CREATE TABLE iso (id INTEGER, v INTEGER)"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := db.ExecContext(c, "INSERT INTO iso VALUES (1, 10)"); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Serializable read-only transaction.
	tx, err := db.BeginTx(c, &sql.TxOptions{Isolation: sql.LevelSerializable, ReadOnly: true})
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	var v int
	if err := tx.QueryRowContext(c, "SELECT v FROM iso WHERE id = 1").Scan(&v); err != nil {
		t.Fatalf("read: %v", err)
	}
	if v != 10 {
		t.Errorf("v = %d, want 10", v)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	// Read committed level should also work and allow writes.
	tx2, err := db.BeginTx(c, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		t.Fatalf("begin2: %v", err)
	}
	if _, err := tx2.ExecContext(c, "UPDATE iso SET v = 20 WHERE id = 1"); err != nil {
		t.Fatalf("update: %v", err)
	}
	if err := tx2.Commit(); err != nil {
		t.Fatalf("commit2: %v", err)
	}
	if err := db.QueryRowContext(c, "SELECT v FROM iso WHERE id = 1").Scan(&v); err != nil {
		t.Fatalf("final read: %v", err)
	}
	if v != 20 {
		t.Errorf("final v = %d, want 20", v)
	}
}

func TestConnPoolReuse(t *testing.T) {
	// Exercise the pool: many sequential operations reusing connections,
	// verifying ResetSession/IsValid keep connections healthy.
	db := openDB(t)
	c := ctx(t)
	db.SetMaxOpenConns(3)
	if _, err := db.ExecContext(c, "CREATE TABLE pool (n INTEGER)"); err != nil {
		t.Fatalf("create: %v", err)
	}
	for i := 0; i < 50; i++ {
		if _, err := db.ExecContext(c, "INSERT INTO pool VALUES (?)", i); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}
	var n, sum int
	if err := db.QueryRowContext(c, "SELECT COUNT(*), SUM(n) FROM pool").Scan(&n, &sum); err != nil {
		t.Fatalf("aggregate: %v", err)
	}
	if n != 50 || sum != 49*50/2 {
		t.Fatalf("n=%d sum=%d", n, sum)
	}
}
