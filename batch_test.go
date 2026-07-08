package hsql

import (
	"context"
	"testing"
)

func TestPreparedBatch(t *testing.T) {
	db := openDB(t)
	c := ctx(t)
	if _, err := db.ExecContext(c, "CREATE TABLE pb (id INTEGER, name VARCHAR(20))"); err != nil {
		t.Fatalf("create: %v", err)
	}

	conn, err := db.Conn(c)
	if err != nil {
		t.Fatalf("conn: %v", err)
	}
	defer conn.Close()

	rows := [][]any{
		{1, "a"},
		{2, "b"},
		{3, "c"},
	}
	var counts []int64
	err = conn.Raw(func(dc any) error {
		b, ok := dc.(interface {
			ExecPreparedBatch(context.Context, string, [][]any) ([]int64, error)
		})
		if !ok {
			t.Fatal("driver conn does not expose ExecPreparedBatch")
		}
		var e error
		counts, e = b.ExecPreparedBatch(c, "INSERT INTO pb VALUES (?, ?)", rows)
		return e
	})
	if err != nil {
		t.Fatalf("ExecPreparedBatch: %v", err)
	}
	if len(counts) != 3 {
		t.Fatalf("counts = %v, want 3 entries", counts)
	}
	for i, n := range counts {
		if n != 1 {
			t.Errorf("count[%d] = %d, want 1", i, n)
		}
	}

	// Verify through the same *sql.Conn: the pool has a single connection
	// (MaxOpenConns(1)) which is still held here.
	var total, sum int
	if err := conn.QueryRowContext(c, "SELECT COUNT(*), SUM(id) FROM pb").Scan(&total, &sum); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if total != 3 || sum != 6 {
		t.Fatalf("total=%d sum=%d, want 3 and 6", total, sum)
	}
}
