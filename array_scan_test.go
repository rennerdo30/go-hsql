package hsql

import "testing"

func TestScanArrayUnit(t *testing.T) {
	var ints []int64
	if err := ScanArray(&ints).Scan("[1,2,3]"); err != nil {
		t.Fatalf("int scan: %v", err)
	}
	if len(ints) != 3 || ints[0] != 1 || ints[2] != 3 {
		t.Fatalf("ints = %v", ints)
	}

	var strs []string
	if err := ScanArray(&strs).Scan("[a,b,c]"); err != nil {
		t.Fatalf("string scan: %v", err)
	}
	if len(strs) != 3 || strs[1] != "b" {
		t.Fatalf("strs = %v", strs)
	}

	// NULL element -> zero value.
	var withNull []int64
	if err := ScanArray(&withNull).Scan("[7,NULL,8]"); err != nil {
		t.Fatalf("null scan: %v", err)
	}
	if len(withNull) != 3 || withNull[1] != 0 {
		t.Fatalf("withNull = %v", withNull)
	}

	// NULL array -> nil slice.
	var empty []int64
	if err := ScanArray(&empty).Scan(nil); err != nil {
		t.Fatalf("nil scan: %v", err)
	}
	if empty != nil {
		t.Fatalf("expected nil slice, got %v", empty)
	}

	// Bad destination.
	var notslice int
	if err := ScanArray(&notslice).Scan("[1]"); err == nil {
		t.Fatal("expected error for non-slice destination")
	}
}

func TestScanArrayFromQuery(t *testing.T) {
	db := openDB(t)
	c := ctx(t)
	if _, err := db.ExecContext(c, "CREATE TABLE arr (id INTEGER, xs INTEGER ARRAY)"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := db.ExecContext(c, "INSERT INTO arr VALUES (1, ARRAY[10,20,30])"); err != nil {
		t.Fatalf("insert: %v", err)
	}
	var xs []int64
	if err := db.QueryRowContext(c, "SELECT xs FROM arr WHERE id = 1").Scan(ScanArray(&xs)); err != nil {
		t.Fatalf("scan array: %v", err)
	}
	if len(xs) != 3 || xs[0] != 10 || xs[2] != 30 {
		t.Fatalf("xs = %v, want [10 20 30]", xs)
	}
}
