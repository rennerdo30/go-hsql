package hsql

import "testing"

func TestLastInsertIdDirect(t *testing.T) {
	db := openDB(t)
	c := ctx(t)

	if _, err := db.ExecContext(c, `CREATE TABLE gk (
		id INTEGER GENERATED ALWAYS AS IDENTITY (START WITH 1) PRIMARY KEY,
		name VARCHAR(20)
	)`); err != nil {
		t.Fatalf("create: %v", err)
	}

	res, err := db.ExecContext(c, "INSERT INTO gk (name) VALUES ('alpha')")
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("LastInsertId: %v", err)
	}
	if id != 1 {
		t.Fatalf("LastInsertId = %d, want 1", id)
	}

	res2, err := db.ExecContext(c, "INSERT INTO gk (name) VALUES ('beta')")
	if err != nil {
		t.Fatalf("insert2: %v", err)
	}
	id2, _ := res2.LastInsertId()
	if id2 != id+1 {
		t.Fatalf("second id = %d, want %d", id2, id+1)
	}

	// A non-insert exec must still work with keys requested and simply have no
	// LastInsertId.
	if _, err := db.ExecContext(c, "UPDATE gk SET name = 'x' WHERE id = "+itoa(id)); err != nil {
		t.Fatalf("update with keys requested: %v", err)
	}
}

func TestLastInsertIdPrepared(t *testing.T) {
	db := openDB(t)
	c := ctx(t)

	if _, err := db.ExecContext(c, `CREATE TABLE gp (
		id INTEGER GENERATED ALWAYS AS IDENTITY (START WITH 100) PRIMARY KEY,
		name VARCHAR(20)
	)`); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Prepared INSERT with a parameter should still yield a generated key.
	stmt, err := db.PrepareContext(c, "INSERT INTO gp (name) VALUES (?)")
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	defer stmt.Close()
	res, err := stmt.ExecContext(c, "gamma")
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if id, err := res.LastInsertId(); err != nil || id != 100 {
		t.Fatalf("prepared LastInsertId = %d, err=%v; want 100", id, err)
	}

	// A prepared SELECT (also prepared with keys requested) must still query.
	q, err := db.PrepareContext(c, "SELECT name FROM gp WHERE id = ?")
	if err != nil {
		t.Fatalf("prepare select: %v", err)
	}
	defer q.Close()
	var name string
	if err := q.QueryRowContext(c, 100).Scan(&name); err != nil {
		t.Fatalf("prepared query: %v", err)
	}
	if name != "gamma" {
		t.Fatalf("name = %q, want gamma", name)
	}
}

func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var b [20]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
