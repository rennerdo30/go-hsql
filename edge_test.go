package hsql

import (
	"bytes"
	"database/sql"
	"fmt"
	"math"
	"strings"
	"sync"
	"testing"
)

// TestLargeLOBChunking exercises the multi-chunk LOB fetch loop (maxLobChunk =
// 1 MiB) with payloads several megabytes in size.
func TestLargeLOBChunking(t *testing.T) {
	db := openDB(t)
	c := ctx(t)
	if _, err := db.ExecContext(c, "CREATE TABLE big (id INTEGER, b BLOB, t CLOB)"); err != nil {
		t.Fatalf("create: %v", err)
	}
	// ~3.3 MiB each: forces 4 GET_BYTES / GET_CHARS chunks.
	bin := bytes.Repeat([]byte{0xDE, 0xAD, 0xBE, 0xEF}, 850000)
	txt := strings.Repeat("abcdefghij", 340000)
	if _, err := db.ExecContext(c, "INSERT INTO big VALUES (?, ?, ?)",
		1, NewBlob(bytes.NewReader(bin), int64(len(bin))), NewClob(strings.NewReader(txt), int64(len(txt)))); err != nil {
		t.Fatalf("insert large LOBs: %v", err)
	}
	var gotB []byte
	var gotT string
	if err := db.QueryRowContext(c, "SELECT b, t FROM big WHERE id = 1").Scan(&gotB, &gotT); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if !bytes.Equal(gotB, bin) {
		t.Fatalf("large BLOB mismatch: got %d bytes want %d", len(gotB), len(bin))
	}
	if gotT != txt {
		t.Fatalf("large CLOB mismatch: got %d chars want %d", len(gotT), len(txt))
	}
}

// TestNumericBoundaries round-trips extreme integer and floating-point values.
func TestNumericBoundaries(t *testing.T) {
	db := openDB(t)
	c := ctx(t)
	if _, err := db.ExecContext(c, "CREATE TABLE nb (i INTEGER, bi BIGINT, d DOUBLE, dec DECIMAL(20,5))"); err != nil {
		t.Fatalf("create: %v", err)
	}
	cases := []struct {
		i   int32
		bi  int64
		d   float64
		dec string
	}{
		{math.MaxInt32, math.MaxInt64, math.MaxFloat64, "123456789012345.67890"},
		{math.MinInt32, math.MinInt64, -math.MaxFloat64, "-123456789012345.67890"},
		{0, 0, math.SmallestNonzeroFloat64, "0.00000"},
	}
	for idx, tc := range cases {
		if _, err := db.ExecContext(c, "INSERT INTO nb VALUES (?,?,?,?)", tc.i, tc.bi, tc.d, tc.dec); err != nil {
			t.Fatalf("insert %d: %v", idx, err)
		}
		var gi int64
		var gbi int64
		var gd float64
		var gdec string
		if err := db.QueryRowContext(c, "SELECT i,bi,d,dec FROM nb WHERE i = ?", tc.i).Scan(&gi, &gbi, &gd, &gdec); err != nil {
			t.Fatalf("scan %d: %v", idx, err)
		}
		if gi != int64(tc.i) || gbi != tc.bi || gd != tc.d {
			t.Errorf("case %d: got (%d,%d,%v) want (%d,%d,%v)", idx, gi, gbi, gd, tc.i, tc.bi, tc.d)
		}
		if gdec != tc.dec {
			t.Errorf("case %d: decimal got %q want %q", idx, gdec, tc.dec)
		}
	}
}

// TestEmptyAndZeroLength covers empty strings and zero-length binaries.
func TestEmptyAndZeroLength(t *testing.T) {
	db := openDB(t)
	c := ctx(t)
	if _, err := db.ExecContext(c, "CREATE TABLE ez (vc VARCHAR(10), bin VARBINARY(10))"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := db.ExecContext(c, "INSERT INTO ez VALUES (?, ?)", "", []byte{}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	var vc string
	var bin []byte
	if err := db.QueryRowContext(c, "SELECT vc, bin FROM ez").Scan(&vc, &bin); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if vc != "" {
		t.Errorf("empty string round-trip = %q", vc)
	}
	if len(bin) != 0 {
		t.Errorf("zero-length binary round-trip = %v", bin)
	}
}

// TestNullParams binds NULL parameters into nullable columns and reads them back.
func TestNullParams(t *testing.T) {
	db := openDB(t)
	c := ctx(t)
	if _, err := db.ExecContext(c, "CREATE TABLE np (a INTEGER, b VARCHAR(10), d DOUBLE, ts TIMESTAMP)"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := db.ExecContext(c, "INSERT INTO np VALUES (?,?,?,?)", nil, nil, nil, nil); err != nil {
		t.Fatalf("insert nulls: %v", err)
	}
	var a sql.NullInt64
	var b sql.NullString
	var d sql.NullFloat64
	var ts sql.NullTime
	if err := db.QueryRowContext(c, "SELECT a,b,d,ts FROM np").Scan(&a, &b, &d, &ts); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if a.Valid || b.Valid || d.Valid || ts.Valid {
		t.Fatalf("expected all NULL, got a=%v b=%v d=%v ts=%v", a, b, d, ts)
	}
}

// TestConcurrentConnections runs many goroutines against a multi-connection pool.
func TestConcurrentConnections(t *testing.T) {
	db := openDB(t)
	db.SetMaxOpenConns(4)
	c := ctx(t)
	if _, err := db.ExecContext(c, "CREATE TABLE conc (g INTEGER, n INTEGER)"); err != nil {
		t.Fatalf("create: %v", err)
	}
	const workers, perWorker = 8, 25
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				if _, err := db.ExecContext(c, "INSERT INTO conc VALUES (?, ?)", g, i); err != nil {
					errs <- fmt.Errorf("worker %d insert %d: %w", g, i, err)
					return
				}
			}
			var cnt int
			if err := db.QueryRowContext(c, "SELECT COUNT(*) FROM conc WHERE g = ?", g).Scan(&cnt); err != nil {
				errs <- fmt.Errorf("worker %d count: %w", g, err)
				return
			}
			if cnt != perWorker {
				errs <- fmt.Errorf("worker %d saw %d rows, want %d", g, cnt, perWorker)
			}
		}(w)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	var total int
	if err := db.QueryRowContext(c, "SELECT COUNT(*) FROM conc").Scan(&total); err != nil {
		t.Fatalf("total: %v", err)
	}
	if total != workers*perWorker {
		t.Fatalf("total = %d, want %d", total, workers*perWorker)
	}
}

// TestPagingBoundaries checks result paging at exact block boundaries.
func TestPagingBoundaries(t *testing.T) {
	for _, total := range []int{0, 1, 9, 10, 11, 20, 21} {
		t.Run(fmt.Sprintf("rows=%d", total), func(t *testing.T) {
			srv := startTestServer(t)
			db, err := sql.Open("hsql", srv.dsn+"?fetchsize=10")
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			defer db.Close()
			db.SetMaxOpenConns(1)
			c := ctx(t)
			if _, err := db.ExecContext(c, "CREATE TABLE pg (n INTEGER)"); err != nil {
				t.Fatalf("create: %v", err)
			}
			for i := 0; i < total; i++ {
				if _, err := db.ExecContext(c, "INSERT INTO pg VALUES (?)", i); err != nil {
					t.Fatalf("insert: %v", err)
				}
			}
			rows, err := db.QueryContext(c, "SELECT n FROM pg ORDER BY n")
			if err != nil {
				t.Fatalf("query: %v", err)
			}
			defer rows.Close()
			got := 0
			for rows.Next() {
				var n int
				if err := rows.Scan(&n); err != nil {
					t.Fatalf("scan: %v", err)
				}
				if n != got {
					t.Fatalf("row %d = %d, out of order", got, n)
				}
				got++
			}
			if err := rows.Err(); err != nil {
				t.Fatalf("rows err: %v", err)
			}
			if got != total {
				t.Fatalf("got %d rows, want %d", got, total)
			}
		})
	}
}
