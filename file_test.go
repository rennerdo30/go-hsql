package hsql

import (
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// startFileServer boots the vendored HSQLDB jar backed by a file database at
// dbPath (no "file:" prefix) on the given port, returning the running command.
func startFileServer(t *testing.T, dbPath string, port int) *exec.Cmd {
	t.Helper()
	jar := filepath.Join(".hsqldb", "hsqldb.jar")
	if _, err := os.Stat(jar); err != nil {
		t.Skipf("hsqldb.jar not found; skipping file-backed test")
	}
	java, err := exec.LookPath("java")
	if err != nil {
		t.Skip("java not found; skipping file-backed test")
	}
	cmd := exec.Command(java, "-cp", jar, "org.hsqldb.server.Server",
		"--database.0", "file:"+dbPath, "--dbname.0", "fdb",
		"--port", fmt.Sprintf("%d", port), "--silent", "true")
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start file server: %v", err)
	}
	if err := waitPort(port, 15*time.Second); err != nil {
		_ = cmd.Process.Kill()
		t.Fatalf("file server not ready: %v", err)
	}
	return cmd
}

// TestFileBackedPersistence proves that a file-backed (on-disk) server database
// works through the driver and that committed data survives a full server
// restart — the counterpart to the in-memory (mem:) databases the rest of the
// suite exercises.
func TestFileBackedPersistence(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "store")

	// --- Phase 1: create + insert, then shut the database down cleanly. ---
	port1 := freePort(t)
	cmd1 := startFileServer(t, dbPath, port1)
	dsn1 := fmt.Sprintf("hsql://SA@localhost:%d/fdb", port1)

	db1, err := sql.Open("hsql", dsn1)
	if err != nil {
		t.Fatalf("open phase1: %v", err)
	}
	c := ctx(t)
	if _, err := db1.ExecContext(c, "CREATE TABLE acct (id INTEGER PRIMARY KEY, bal DECIMAL(10,2))"); err != nil {
		t.Fatalf("create: %v", err)
	}
	for i := 1; i <= 3; i++ {
		if _, err := db1.ExecContext(c, "INSERT INTO acct VALUES (?, ?)", i, fmt.Sprintf("%d.50", i*100)); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}
	// SHUTDOWN flushes the database to disk and stops the server cleanly.
	if _, err := db1.ExecContext(c, "SHUTDOWN"); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	db1.Close()
	_, _ = cmd1.Process.Wait()

	// Confirm the on-disk files exist.
	if _, err := os.Stat(dbPath + ".script"); err != nil {
		t.Fatalf("expected on-disk .script file: %v", err)
	}

	// --- Phase 2: restart on the same files; data must still be there. ---
	port2 := freePort(t)
	cmd2 := startFileServer(t, dbPath, port2)
	defer func() { _ = cmd2.Process.Kill(); _, _ = cmd2.Process.Wait() }()
	dsn2 := fmt.Sprintf("hsql://SA@localhost:%d/fdb", port2)

	db2, err := sql.Open("hsql", dsn2)
	if err != nil {
		t.Fatalf("open phase2: %v", err)
	}
	defer db2.Close()

	var count int
	var bal string
	if err := db2.QueryRowContext(c, "SELECT COUNT(*) FROM acct").Scan(&count); err != nil {
		t.Fatalf("count after restart: %v", err)
	}
	if count != 3 {
		t.Fatalf("rows after restart = %d, want 3 (persistence failed)", count)
	}
	if err := db2.QueryRowContext(c, "SELECT bal FROM acct WHERE id = 2").Scan(&bal); err != nil {
		t.Fatalf("read after restart: %v", err)
	}
	if bal != "200.50" {
		t.Fatalf("bal after restart = %q, want 200.50", bal)
	}
}
