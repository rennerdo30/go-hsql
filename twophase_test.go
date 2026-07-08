package hsql

import (
	"context"
	"database/sql/driver"
	"testing"
)

func TestPrepareCommit(t *testing.T) {
	db := openDB(t)
	c := ctx(t)
	if _, err := db.ExecContext(c, "CREATE TABLE tp (n INTEGER)"); err != nil {
		t.Fatalf("create: %v", err)
	}

	sqlConn, err := db.Conn(c)
	if err != nil {
		t.Fatalf("conn: %v", err)
	}
	defer sqlConn.Close()

	err = sqlConn.Raw(func(dc any) error {
		pc, ok := dc.(interface {
			PrepareCommit(context.Context) error
		})
		if !ok {
			t.Fatal("driver conn does not expose PrepareCommit")
		}
		cn := dc.(*conn)
		// Begin a transaction, do work, prepare, then commit (2PC on a local DB).
		txn, err := cn.BeginTx(c, driver.TxOptions{})
		if err != nil {
			return err
		}
		if _, err := cn.execCtx(c, cn.newExecDirect("INSERT INTO tp VALUES (1)")); err != nil {
			return err
		}
		if err := pc.PrepareCommit(c); err != nil {
			return err
		}
		return txn.Commit()
	})
	if err != nil {
		t.Fatalf("2PC flow: %v", err)
	}

	var n int
	if err := sqlConn.QueryRowContext(c, "SELECT COUNT(*) FROM tp").Scan(&n); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if n != 1 {
		t.Fatalf("count = %d, want 1", n)
	}
}
