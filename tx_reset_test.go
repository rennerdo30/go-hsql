package hsql

import (
	"database/sql"
	"testing"
)

// TestReadUncommittedDoesNotLeakReadOnly guards against a real connection-reuse
// bug: HSQLDB makes a READ UNCOMMITTED transaction session-read-only, so without
// a reset the next writer on the same pooled connection would fail.
func TestReadUncommittedDoesNotLeakReadOnly(t *testing.T) {
	db := openDB(t) // MaxOpenConns(1): forces reuse of the same connection
	c := ctx(t)
	if _, err := db.ExecContext(c, "CREATE TABLE rl (n INTEGER)"); err != nil {
		t.Fatalf("create: %v", err)
	}

	// A read-only READ UNCOMMITTED transaction.
	tx, err := db.BeginTx(c, &sql.TxOptions{Isolation: sql.LevelReadUncommitted})
	if err != nil {
		t.Fatalf("begin RU: %v", err)
	}
	var x int
	if err := tx.QueryRowContext(c, "SELECT COUNT(*) FROM rl").Scan(&x); err != nil {
		t.Fatalf("read: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit RU: %v", err)
	}

	// The same connection must now accept writes again.
	if _, err := db.ExecContext(c, "INSERT INTO rl VALUES (1)"); err != nil {
		t.Fatalf("write after READ UNCOMMITTED leaked read-only: %v", err)
	}

	// And a subsequent default transaction can write too.
	tx2, err := db.BeginTx(c, nil)
	if err != nil {
		t.Fatalf("begin default: %v", err)
	}
	if _, err := tx2.ExecContext(c, "INSERT INTO rl VALUES (2)"); err != nil {
		t.Fatalf("write in default tx after RU: %v", err)
	}
	if err := tx2.Commit(); err != nil {
		t.Fatalf("commit default: %v", err)
	}

	var n int
	if err := db.QueryRowContext(c, "SELECT COUNT(*) FROM rl").Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 2 {
		t.Fatalf("count = %d, want 2", n)
	}
}
