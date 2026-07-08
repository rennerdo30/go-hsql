package hsql

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestContextCancellation(t *testing.T) {
	db := openDB(t)
	db.SetMaxOpenConns(2) // one to run the slow query, one to verify recovery
	setup := ctx(t)

	if _, err := db.ExecContext(setup, "CREATE TABLE big (n INTEGER)"); err != nil {
		t.Fatalf("create: %v", err)
	}
	ins, err := db.PrepareContext(setup, "INSERT INTO big VALUES (?)")
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	for i := 0; i < 1000; i++ {
		if _, err := ins.ExecContext(setup, i); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}
	ins.Close()

	// An O(n^3) cross join over 1000 rows = 10^9 combinations: comfortably long
	// enough to cancel mid-flight.
	cctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(200 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	var count int
	err = db.QueryRowContext(cctx,
		"SELECT COUNT(*) FROM big a JOIN big b ON 1=1 JOIN big c ON 1=1 WHERE a.n+b.n+c.n >= 0").
		Scan(&count)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatalf("expected cancellation error, query completed in %s with count=%d", elapsed, count)
	}
	if !errors.Is(err, context.Canceled) {
		t.Logf("cancellation returned %v (not context.Canceled, but did error)", err)
	}
	if elapsed > 8*time.Second {
		t.Fatalf("cancellation took too long: %s", elapsed)
	}
	t.Logf("query canceled after %s", elapsed)

	// The pool must remain usable afterward (the canceled connection is dropped;
	// a fresh one serves this query).
	var n int
	if err := db.QueryRowContext(ctx(t), "SELECT COUNT(*) FROM big").Scan(&n); err != nil {
		t.Fatalf("query after cancel: %v", err)
	}
	if n != 1000 {
		t.Fatalf("count after cancel = %d, want 1000", n)
	}
}
