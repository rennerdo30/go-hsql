package hsql

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

// TestArrayTextCodec covers the lossless array text form without a server:
// encoding of every element kind and quote-aware parsing back.
func TestArrayTextCodec(t *testing.T) {
	ts := time.Date(2024, 5, 1, 10, 30, 0, 123456789, time.UTC)
	text := encodeArrayText([]any{
		int64(7), nil, 3.5, true, "a,b", `he said "hi"`, "NULL", []byte{0xab, 0x12}, ts,
	})
	want := `[7,NULL,3.5,true,"a,b","he said \"hi\"","NULL",ab12,"2024-05-01T10:30:00.123456789Z"]`
	if text != want {
		t.Fatalf("encoded = %s, want %s", text, want)
	}

	var anys []any
	if err := ScanArray(&anys).Scan(text); err != nil {
		t.Fatalf("scan any: %v", err)
	}
	if len(anys) != 9 || anys[0] != int64(7) || anys[1] != nil || anys[2] != 3.5 ||
		anys[3] != true || anys[4] != "a,b" || anys[5] != `he said "hi"` || anys[6] != "NULL" {
		t.Errorf("anys = %#v", anys)
	}

	// Typed destinations.
	var times []time.Time
	if err := ScanArray(&times).Scan(`["2024-05-01T10:30:00.123456789Z"]`); err != nil {
		t.Fatalf("scan times: %v", err)
	}
	if len(times) != 1 || !times[0].Equal(ts) {
		t.Errorf("times = %v", times)
	}
	var bss [][]byte
	if err := ScanArray(&bss).Scan(`[ab12,"raw"]`); err != nil {
		t.Fatalf("scan bytes: %v", err)
	}
	if len(bss) != 2 || bss[0][0] != 0xab || string(bss[1]) != "raw" {
		t.Errorf("bss = %v", bss)
	}
	var strs []string
	if err := ScanArray(&strs).Scan(`["x","y,z"]`); err != nil {
		t.Fatalf("scan strings: %v", err)
	}
	if len(strs) != 2 || strs[1] != "y,z" {
		t.Errorf("strs = %v", strs)
	}

	// Malformed inputs error rather than desync.
	if err := ScanArray(&strs).Scan(`["unterminated]`); err == nil {
		t.Error("expected error for unterminated quote")
	}
	if err := ScanArray(&strs).Scan(`no brackets`); err == nil {
		t.Error("expected error for missing brackets")
	}
}

// TestCallOutParameters covers OUT and INOUT parameters of a PSM procedure via
// sql.Out, delivered through the CALL_RESPONSE wire path.
func TestCallOutParameters(t *testing.T) {
	db := openDB(t)
	c := ctx(t)
	if _, err := db.ExecContext(c, `CREATE PROCEDURE add_and_double(IN a INTEGER, OUT b INTEGER, INOUT x INTEGER)
		BEGIN ATOMIC
			SET b = a + 10;
			SET x = x * 2;
		END`); err != nil {
		t.Fatalf("create procedure: %v", err)
	}

	var b int64
	x := int64(21)
	if _, err := db.ExecContext(c, "CALL add_and_double(?, ?, ?)",
		5, sql.Out{Dest: &b}, sql.Out{Dest: &x, In: true}); err != nil {
		t.Fatalf("call: %v", err)
	}
	if b != 15 {
		t.Errorf("OUT b = %d, want 15", b)
	}
	if x != 42 {
		t.Errorf("INOUT x = %d, want 42", x)
	}

	// OUT into sql.Null* scanner destinations, including a NULL value.
	if _, err := db.ExecContext(c, `CREATE PROCEDURE give_null(OUT s VARCHAR(10))
		BEGIN ATOMIC
			SET s = NULL;
		END`); err != nil {
		t.Fatalf("create procedure: %v", err)
	}
	var ns sql.NullString
	if _, err := db.ExecContext(c, "CALL give_null(?)", sql.Out{Dest: &ns}); err != nil {
		t.Fatalf("call give_null: %v", err)
	}
	if ns.Valid {
		t.Errorf("OUT s = %+v, want NULL", ns)
	}
}

// TestCallProcedureResultSet verifies that a procedure returning a result set
// yields the chained DATA rows (not the CALL_RESPONSE parameter row) and that
// an OUT parameter can be read from the same call.
func TestCallProcedureResultSet(t *testing.T) {
	db := openDB(t)
	c := ctx(t)
	if _, err := db.ExecContext(c, "CREATE TABLE pt (id INTEGER, name VARCHAR(20))"); err != nil {
		t.Fatalf("create table: %v", err)
	}
	if _, err := db.ExecContext(c, "INSERT INTO pt VALUES (1, 'one'), (2, 'two')"); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if _, err := db.ExecContext(c, `CREATE PROCEDURE list_pt(OUT total INTEGER)
		READS SQL DATA DYNAMIC RESULT SETS 1
		BEGIN ATOMIC
			DECLARE result CURSOR WITH RETURN FOR SELECT id, name FROM pt ORDER BY id;
			SET total = (SELECT COUNT(*) FROM pt);
			OPEN result;
		END`); err != nil {
		t.Fatalf("create procedure: %v", err)
	}

	var total int64
	rows, err := db.QueryContext(c, "CALL list_pt(?)", sql.Out{Dest: &total})
	if err != nil {
		t.Fatalf("query call: %v", err)
	}
	defer rows.Close()
	var ids []int64
	var names []string
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			t.Fatalf("scan: %v", err)
		}
		ids = append(ids, id)
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	if len(ids) != 2 || ids[0] != 1 || names[1] != "two" {
		t.Errorf("rows = %v %v, want [1 2] [one two]", ids, names)
	}
	if total != 2 {
		t.Errorf("OUT total = %d, want 2", total)
	}
}

// TestArrayLosslessText covers the lossless array text form: string elements
// containing commas, quotes, brackets and the literal NULL round-trip exactly,
// and temporal arrays scan into []time.Time.
func TestArrayLosslessText(t *testing.T) {
	db := openDB(t)
	c := ctx(t)
	if _, err := db.ExecContext(c, "CREATE TABLE la (id INTEGER, ss VARCHAR(50) ARRAY, ts TIMESTAMP ARRAY)"); err != nil {
		t.Fatalf("create table: %v", err)
	}
	want := []string{"a,b", `he said "hi"`, "NULL", "[x]"}
	if _, err := db.ExecContext(c,
		"INSERT INTO la VALUES (1, ?, ARRAY[TIMESTAMP '2024-05-01 10:30:00', TIMESTAMP '2024-05-02 11:45:30'])",
		NewArray(want[0], want[1], want[2], want[3])); err != nil {
		t.Fatalf("insert: %v", err)
	}

	var ss []string
	var ts []time.Time
	if err := db.QueryRowContext(c, "SELECT ss, ts FROM la WHERE id = 1").Scan(ScanArray(&ss), ScanArray(&ts)); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(ss) != len(want) {
		t.Fatalf("ss = %v, want %v", ss, want)
	}
	for i := range want {
		if ss[i] != want[i] {
			t.Errorf("ss[%d] = %q, want %q", i, ss[i], want[i])
		}
	}
	if len(ts) != 2 || !ts[0].Equal(time.Date(2024, 5, 1, 10, 30, 0, 0, time.UTC)) {
		t.Errorf("ts = %v", ts)
	}

	// []any destination with mixed inference, and a NULL element.
	if _, err := db.ExecContext(c, "CREATE TABLE lb (id INTEGER, xs INTEGER ARRAY)"); err != nil {
		t.Fatalf("create table: %v", err)
	}
	if _, err := db.ExecContext(c, "INSERT INTO lb VALUES (1, ARRAY[7, NULL, 8])"); err != nil {
		t.Fatalf("insert: %v", err)
	}
	var anys []any
	if err := db.QueryRowContext(c, "SELECT xs FROM lb WHERE id = 1").Scan(ScanArray(&anys)); err != nil {
		t.Fatalf("scan any: %v", err)
	}
	if len(anys) != 3 || anys[0] != int64(7) || anys[1] != nil || anys[2] != int64(8) {
		t.Errorf("anys = %#v", anys)
	}
}

// TestUpdatableResultSet exercises the UPDATE_RESULT sub-protocol: update,
// delete and insert through a server-side cursor.
func TestUpdatableResultSet(t *testing.T) {
	db := openDB(t)
	c := ctx(t)
	if _, err := db.ExecContext(c, "CREATE TABLE ut (id INTEGER PRIMARY KEY, name VARCHAR(20), n INTEGER)"); err != nil {
		t.Fatalf("create table: %v", err)
	}
	if _, err := db.ExecContext(c, "INSERT INTO ut VALUES (1, 'one', 10), (2, 'two', 20), (3, 'three', 30)"); err != nil {
		t.Fatalf("insert: %v", err)
	}

	conn, err := db.Conn(c)
	if err != nil {
		t.Fatalf("conn: %v", err)
	}
	defer conn.Close()

	err = conn.Raw(func(dc any) error {
		q := dc.(interface {
			QueryUpdatable(ctx context.Context, query string, args ...any) (*UpdatableRows, error)
		})
		// Note: HSQLDB refuses to make ORDER BY queries updatable
		// (QuerySpecification.setUpdatability), so select in table order.
		rows, err := q.QueryUpdatable(c, "SELECT id, name, n FROM ut")
		if err != nil {
			return err
		}
		defer rows.Close()

		if got := rows.Columns(); len(got) != 3 || got[0] != "ID" {
			t.Errorf("columns = %v", got)
		}
		if rows.Len() != 3 {
			t.Fatalf("len = %d, want 3", rows.Len())
		}

		// Row 1: update name and n.
		if !rows.Next() {
			t.Fatal("no first row")
		}
		if err := rows.UpdateRow(c, map[string]any{"name": "ONE", "n": 11}); err != nil {
			t.Fatalf("update row: %v", err)
		}
		// The client copy reflects the update.
		var id, n int64
		var name string
		if err := rows.Scan(&id, &name, &n); err != nil {
			t.Fatalf("scan after update: %v", err)
		}
		if id != 1 || name != "ONE" || n != 11 {
			t.Errorf("row after update = %d %q %d, want 1 ONE 11", id, name, n)
		}

		// Row 2: delete.
		if !rows.Next() {
			t.Fatal("no second row")
		}
		if err := rows.DeleteRow(c); err != nil {
			t.Fatalf("delete row: %v", err)
		}
		if rows.Len() != 2 {
			t.Errorf("len after delete = %d, want 2", rows.Len())
		}

		// Insert a new row through the cursor.
		if err := rows.InsertRow(c, map[string]any{"id": 4, "name": "four", "n": 40}); err != nil {
			t.Fatalf("insert row: %v", err)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("raw: %v", err)
	}
	// Release the raw connection: the pool is capped at one connection, so the
	// verification query below would otherwise starve.
	conn.Close()

	// Verify against the table with a plain query.
	got := map[int64]string{}
	rows, err := db.QueryContext(c, "SELECT id, name FROM ut ORDER BY id")
	if err != nil {
		t.Fatalf("verify query: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			t.Fatalf("verify scan: %v", err)
		}
		got[id] = name
	}
	if len(got) != 3 || got[1] != "ONE" || got[4] != "four" {
		t.Errorf("table state = %v, want map[1:ONE 3:three 4:four]", got)
	}
	if _, deleted := got[2]; deleted {
		t.Error("row 2 still present, want deleted")
	}
}

// TestSessionInfo covers the GETSESSIONATTR round-trips.
func TestSessionInfo(t *testing.T) {
	db := openDB(t)
	c := ctx(t)
	conn, err := db.Conn(c)
	if err != nil {
		t.Fatalf("conn: %v", err)
	}
	defer conn.Close()

	err = conn.Raw(func(dc any) error {
		s := dc.(interface {
			SessionInfo(ctx context.Context) (*SessionInfo, error)
		})
		info, err := s.SessionInfo(c)
		if err != nil {
			return err
		}
		if !info.AutoCommit {
			t.Error("AutoCommit = false, want true")
		}
		if info.ReadOnly {
			t.Error("ReadOnly = true, want false")
		}
		if info.Isolation != sql.LevelReadCommitted {
			t.Errorf("Isolation = %v, want ReadCommitted", info.Isolation)
		}
		if info.Catalog == "" {
			t.Error("Catalog is empty")
		}
		if info.TimeZone == "" {
			t.Error("TimeZone is empty")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("raw: %v", err)
	}
}
