package hsql

import (
	"context"
	"database/sql"
	"testing"
)

func TestColumnTypes(t *testing.T) {
	db := openDB(t)
	c := ctx(t)
	if _, err := db.ExecContext(c, "CREATE TABLE ct (a INTEGER NOT NULL, b VARCHAR(30), d DOUBLE, n DECIMAL(12, 3))"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := db.ExecContext(c, "INSERT INTO ct VALUES (1, 'x', 2.0, 123.456)"); err != nil {
		t.Fatalf("insert: %v", err)
	}
	rows, err := db.QueryContext(c, "SELECT a, b, d, n FROM ct")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()

	cts, err := rows.ColumnTypes()
	if err != nil {
		t.Fatalf("column types: %v", err)
	}
	if len(cts) != 4 {
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
	if length, ok := cts[1].Length(); !ok || length != 30 {
		t.Errorf("col b length = %d, ok=%v; want 30,true", length, ok)
	}
	if precision, scale, ok := cts[3].DecimalSize(); !ok || precision != 12 || scale != 3 {
		t.Errorf("col n decimal size = (%d,%d,%v), want (12,3,true)", precision, scale, ok)
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

func TestResetSessionRollsBackAndRestoresAutocommit(t *testing.T) {
	db := openDB(t)
	c := ctx(t)
	db.SetMaxOpenConns(1)

	if _, err := db.ExecContext(c, "CREATE TABLE reset_pool (n INTEGER)"); err != nil {
		t.Fatalf("create: %v", err)
	}

	conn, err := db.Conn(c)
	if err != nil {
		t.Fatalf("conn: %v", err)
	}
	if _, err := conn.ExecContext(c, "SET AUTOCOMMIT FALSE"); err != nil {
		t.Fatalf("set autocommit false: %v", err)
	}
	if _, err := conn.ExecContext(c, "INSERT INTO reset_pool VALUES (1)"); err != nil {
		t.Fatalf("insert uncommitted: %v", err)
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("close conn: %v", err)
	}

	var count int
	if err := db.QueryRowContext(c, "SELECT COUNT(*) FROM reset_pool").Scan(&count); err != nil {
		t.Fatalf("count after reset: %v", err)
	}
	if count != 0 {
		t.Fatalf("reset did not roll back uncommitted row; count=%d", count)
	}
	if _, err := db.ExecContext(c, "INSERT INTO reset_pool VALUES (2)"); err != nil {
		t.Fatalf("insert after reset: %v", err)
	}
	if err := db.QueryRowContext(c, "SELECT COUNT(*) FROM reset_pool").Scan(&count); err != nil {
		t.Fatalf("final count: %v", err)
	}
	if count != 1 {
		t.Fatalf("autocommit was not restored; count=%d", count)
	}
}

func TestRawSavepoints(t *testing.T) {
	db := openDB(t)
	c := ctx(t)
	db.SetMaxOpenConns(1)

	if _, err := db.ExecContext(c, "CREATE TABLE sp (n INTEGER)"); err != nil {
		t.Fatalf("create: %v", err)
	}

	conn, err := db.Conn(c)
	if err != nil {
		t.Fatalf("conn: %v", err)
	}

	tx, err := conn.BeginTx(c, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := tx.ExecContext(c, "INSERT INTO sp VALUES (1)"); err != nil {
		t.Fatalf("insert before savepoint: %v", err)
	}
	if err := conn.Raw(func(dc any) error {
		return dc.(interface {
			Savepoint(context.Context, string) error
		}).Savepoint(c, "s1")
	}); err != nil {
		t.Fatalf("savepoint: %v", err)
	}
	if _, err := tx.ExecContext(c, "INSERT INTO sp VALUES (2)"); err != nil {
		t.Fatalf("insert after savepoint: %v", err)
	}
	if err := conn.Raw(func(dc any) error {
		return dc.(interface {
			RollbackToSavepoint(context.Context, string) error
		}).RollbackToSavepoint(c, "s1")
	}); err != nil {
		t.Fatalf("rollback to savepoint: %v", err)
	}
	if err := conn.Raw(func(dc any) error {
		return dc.(interface {
			ReleaseSavepoint(context.Context, string) error
		}).ReleaseSavepoint(c, "s1")
	}); err != nil {
		t.Fatalf("release savepoint: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("conn close: %v", err)
	}

	var count, sum int
	if err := db.QueryRowContext(c, "SELECT COUNT(*), COALESCE(SUM(n), 0) FROM sp").Scan(&count, &sum); err != nil {
		t.Fatalf("aggregate: %v", err)
	}
	if count != 1 || sum != 1 {
		t.Fatalf("savepoint rollback left count=%d sum=%d, want 1/1", count, sum)
	}
}
