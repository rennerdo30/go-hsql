package hsql

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// testServer manages a locally-launched HSQLDB server for integration tests.
type testServer struct {
	cmd  *exec.Cmd
	port int
	dsn  string
}

// startTestServer boots the vendored HSQLDB jar with an in-memory database on a
// free port. It skips the test if Java or the jar are unavailable.
func startTestServer(t *testing.T) *testServer {
	t.Helper()

	jar := filepath.Join(".hsqldb", "hsqldb.jar")
	if _, err := os.Stat(jar); err != nil {
		t.Skipf("hsqldb.jar not found at %s (run the build step); skipping integration test", jar)
	}
	java, err := exec.LookPath("java")
	if err != nil {
		t.Skip("java not found on PATH; skipping integration test")
	}

	port := freePort(t)
	cmd := exec.Command(java,
		"-cp", jar,
		"org.hsqldb.server.Server",
		"--database.0", "mem:testdb",
		"--dbname.0", "testdb",
		"--port", fmt.Sprintf("%d", port),
		"--silent", "true",
		"--trace", "false",
	)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting hsqldb server: %v", err)
	}

	srv := &testServer{
		cmd:  cmd,
		port: port,
		dsn:  fmt.Sprintf("hsql://SA@localhost:%d/testdb", port),
	}
	if err := waitPort(port, 15*time.Second); err != nil {
		_ = cmd.Process.Kill()
		t.Fatalf("hsqldb server did not become ready: %v", err)
	}
	t.Cleanup(func() { srv.stop() })
	return srv
}

func (s *testServer) stop() {
	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
		_, _ = s.cmd.Process.Wait()
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatalf("finding free port: %v", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func waitPort(port int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	addr := fmt.Sprintf("localhost:%d", port)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, time.Second)
		if err == nil {
			conn.Close()
			// The port is open, but the DB may still be initializing; give it a
			// brief moment.
			time.Sleep(200 * time.Millisecond)
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("port %d not open after %s", port, timeout)
}

// ctx returns a short-lived context for test operations.
func ctx(t *testing.T) context.Context {
	t.Helper()
	c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return c
}
