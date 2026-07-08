package hsql

import (
	"database/sql"
	"testing"
)

func openDB(t *testing.T) *sql.DB {
	t.Helper()
	srv := startTestServer(t)
	db, err := sql.Open("hsql", srv.dsn)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1) // keep a single connection for deterministic tracing
	return db
}

func TestConnectAndPing(t *testing.T) {
	db := openDB(t)
	if err := db.PingContext(ctx(t)); err != nil {
		t.Fatalf("ping: %v", err)
	}
}

func TestSimpleValuesQuery(t *testing.T) {
	db := openDB(t)
	var got int
	if err := db.QueryRowContext(ctx(t), "VALUES(42)").Scan(&got); err != nil {
		t.Fatalf("query: %v", err)
	}
	if got != 42 {
		t.Fatalf("got %d, want 42", got)
	}
}

func TestCreateInsertSelect(t *testing.T) {
	db := openDB(t)
	c := ctx(t)

	if _, err := db.ExecContext(c, `CREATE TABLE person (
		id INTEGER PRIMARY KEY,
		name VARCHAR(100),
		height DOUBLE,
		active BOOLEAN
	)`); err != nil {
		t.Fatalf("create: %v", err)
	}

	res, err := db.ExecContext(c, "INSERT INTO person VALUES (1, 'Ada', 1.70, TRUE)")
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		t.Fatalf("rows affected = %d, want 1", n)
	}

	var (
		id     int
		name   string
		height float64
		active bool
	)
	row := db.QueryRowContext(c, "SELECT id, name, height, active FROM person WHERE id = 1")
	if err := row.Scan(&id, &name, &height, &active); err != nil {
		t.Fatalf("select scan: %v", err)
	}
	if id != 1 || name != "Ada" || height != 1.70 || !active {
		t.Fatalf("got (%d,%q,%v,%v), want (1,\"Ada\",1.7,true)", id, name, height, active)
	}
}

func TestPreparedStatement(t *testing.T) {
	db := openDB(t)
	c := ctx(t)

	if _, err := db.ExecContext(c, "CREATE TABLE nums (n INTEGER, label VARCHAR(20))"); err != nil {
		t.Fatalf("create: %v", err)
	}

	stmt, err := db.PrepareContext(c, "INSERT INTO nums VALUES (?, ?)")
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	defer stmt.Close()

	for i := 1; i <= 5; i++ {
		if _, err := stmt.ExecContext(c, i, "item"); err != nil {
			t.Fatalf("exec %d: %v", i, err)
		}
	}

	var count, sum int
	if err := db.QueryRowContext(c, "SELECT COUNT(*), SUM(n) FROM nums").Scan(&count, &sum); err != nil {
		t.Fatalf("aggregate: %v", err)
	}
	if count != 5 || sum != 15 {
		t.Fatalf("count=%d sum=%d, want 5 and 15", count, sum)
	}

	// Parameterized query.
	var label string
	if err := db.QueryRowContext(c, "SELECT label FROM nums WHERE n = ?", 3).Scan(&label); err != nil {
		t.Fatalf("param query: %v", err)
	}
	if label != "item" {
		t.Fatalf("label=%q, want item", label)
	}
}

func TestTransactionCommitRollback(t *testing.T) {
	db := openDB(t)
	c := ctx(t)

	if _, err := db.ExecContext(c, "CREATE TABLE acct (id INTEGER, bal INTEGER)"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := db.ExecContext(c, "INSERT INTO acct VALUES (1, 100)"); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Rollback path.
	tx, err := db.BeginTx(c, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := tx.ExecContext(c, "UPDATE acct SET bal = 0 WHERE id = 1"); err != nil {
		t.Fatalf("update: %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	var bal int
	if err := db.QueryRowContext(c, "SELECT bal FROM acct WHERE id = 1").Scan(&bal); err != nil {
		t.Fatalf("read after rollback: %v", err)
	}
	if bal != 100 {
		t.Fatalf("balance after rollback = %d, want 100", bal)
	}

	// Commit path.
	tx, err = db.BeginTx(c, nil)
	if err != nil {
		t.Fatalf("begin2: %v", err)
	}
	if _, err := tx.ExecContext(c, "UPDATE acct SET bal = 250 WHERE id = 1"); err != nil {
		t.Fatalf("update2: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if err := db.QueryRowContext(c, "SELECT bal FROM acct WHERE id = 1").Scan(&bal); err != nil {
		t.Fatalf("read after commit: %v", err)
	}
	if bal != 250 {
		t.Fatalf("balance after commit = %d, want 250", bal)
	}
}

func TestNullAndError(t *testing.T) {
	db := openDB(t)
	c := ctx(t)

	if _, err := db.ExecContext(c, "CREATE TABLE t (a INTEGER, b VARCHAR(10))"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := db.ExecContext(c, "INSERT INTO t VALUES (NULL, NULL)"); err != nil {
		t.Fatalf("insert null: %v", err)
	}
	var a sql.NullInt64
	var b sql.NullString
	if err := db.QueryRowContext(c, "SELECT a, b FROM t").Scan(&a, &b); err != nil {
		t.Fatalf("scan null: %v", err)
	}
	if a.Valid || b.Valid {
		t.Fatalf("expected NULLs, got a=%v b=%v", a, b)
	}

	// A bad query should surface a *hsql.Error with a SQLState.
	_, err := db.ExecContext(c, "SELECT * FROM does_not_exist")
	if err == nil {
		t.Fatal("expected error for missing table")
	}
	he, ok := err.(*Error)
	if !ok {
		t.Fatalf("expected *hsql.Error, got %T: %v", err, err)
	}
	if he.SQLState == "" {
		t.Fatalf("expected non-empty SQLState, got %+v", he)
	}
}
