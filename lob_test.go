package hsql

import (
	"bytes"
	"encoding/hex"
	"strings"
	"testing"
)

func TestLobRead(t *testing.T) {
	db := openDB(t)
	c := ctx(t)

	if _, err := db.ExecContext(c, `CREATE TABLE docs (
		id INTEGER,
		body CLOB,
		blob_data BLOB
	)`); err != nil {
		t.Fatalf("create: %v", err)
	}

	// A CLOB payload larger than a trivial inline value, and a binary BLOB.
	text := strings.Repeat("The quick brown fox. ", 500) // ~10 KB
	bin := bytes.Repeat([]byte{0x00, 0x01, 0x02, 0xfd, 0xfe, 0xff}, 300)

	insert := "INSERT INTO docs VALUES (1, '" + text + "', X'" + hex.EncodeToString(bin) + "')"
	if _, err := db.ExecContext(c, insert); err != nil {
		t.Fatalf("insert: %v", err)
	}

	var (
		gotText string
		gotBin  []byte
	)
	row := db.QueryRowContext(c, "SELECT body, blob_data FROM docs WHERE id = 1")
	if err := row.Scan(&gotText, &gotBin); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if gotText != text {
		t.Fatalf("CLOB round-trip failed: got %d chars, want %d", len(gotText), len(text))
	}
	if !bytes.Equal(gotBin, bin) {
		t.Fatalf("BLOB round-trip failed: got %d bytes, want %d", len(gotBin), len(bin))
	}
}

func TestLobPreparedWrite(t *testing.T) {
	db := openDB(t)
	c := ctx(t)

	if _, err := db.ExecContext(c, `CREATE TABLE lob_params (
		id INTEGER,
		body CLOB,
		blob_data BLOB
	)`); err != nil {
		t.Fatalf("create: %v", err)
	}

	text := strings.Repeat("prepared 🧪 CLOB ", 300)
	bin := bytes.Repeat([]byte{0x00, 0x7f, 0x80, 0xff}, 700)
	if _, err := db.ExecContext(c, "INSERT INTO lob_params VALUES (?, ?, ?)", 1, text, bin); err != nil {
		t.Fatalf("prepared insert: %v", err)
	}

	var (
		gotText string
		gotBin  []byte
	)
	if err := db.QueryRowContext(c, "SELECT body, blob_data FROM lob_params WHERE id = 1").Scan(&gotText, &gotBin); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if gotText != text {
		t.Fatalf("CLOB prepared round-trip failed: got %d chars, want %d", len(gotText), len(text))
	}
	if !bytes.Equal(gotBin, bin) {
		t.Fatalf("BLOB prepared round-trip failed: got %d bytes, want %d", len(gotBin), len(bin))
	}
}

func TestLobNull(t *testing.T) {
	db := openDB(t)
	c := ctx(t)
	if _, err := db.ExecContext(c, "CREATE TABLE ld (id INTEGER, body CLOB)"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := db.ExecContext(c, "INSERT INTO ld VALUES (1, NULL)"); err != nil {
		t.Fatalf("insert: %v", err)
	}
	var body []byte
	if err := db.QueryRowContext(c, "SELECT body FROM ld WHERE id = 1").Scan(&body); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if body != nil {
		t.Fatalf("expected NULL CLOB, got %v", body)
	}
}
