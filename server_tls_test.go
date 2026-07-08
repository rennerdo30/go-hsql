package hsql

import (
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// startTLSTestServer boots the vendored HSQLDB jar with TLS enabled, generating
// a self-signed keystore with keytool. It returns the server plus the path to
// the exported PEM certificate for the client to trust.
func startTLSTestServer(t *testing.T) (*testServer, string) {
	t.Helper()

	jar := filepath.Join(".hsqldb", "hsqldb.jar")
	if _, err := os.Stat(jar); err != nil {
		t.Skipf("hsqldb.jar not found at %s; skipping TLS test", jar)
	}
	java, err := exec.LookPath("java")
	if err != nil {
		t.Skip("java not found; skipping TLS test")
	}
	keytool, err := exec.LookPath("keytool")
	if err != nil {
		t.Skip("keytool not found; skipping TLS test")
	}

	dir := t.TempDir()
	ksPath := filepath.Join(dir, "server.p12")
	certPath := filepath.Join(dir, "server.crt")
	const pass = "changeit"

	gen := exec.Command(keytool, "-genkeypair", "-alias", "hsqldb",
		"-keyalg", "RSA", "-keysize", "2048", "-validity", "365",
		"-dname", "CN=localhost", "-keystore", ksPath, "-storetype", "PKCS12",
		"-storepass", pass, "-keypass", pass, "-ext", "SAN=dns:localhost,ip:127.0.0.1")
	if out, err := gen.CombinedOutput(); err != nil {
		t.Fatalf("keytool genkeypair: %v\n%s", err, out)
	}
	exp := exec.Command(keytool, "-exportcert", "-alias", "hsqldb",
		"-keystore", ksPath, "-storepass", pass, "-rfc", "-file", certPath)
	if out, err := exp.CombinedOutput(); err != nil {
		t.Fatalf("keytool exportcert: %v\n%s", err, out)
	}

	port := freePort(t)
	cmd := exec.Command(java,
		"-Djavax.net.ssl.keyStore="+ksPath,
		"-Djavax.net.ssl.keyStorePassword="+pass,
		"-Djavax.net.ssl.keyStoreType=PKCS12",
		"-cp", jar, "org.hsqldb.server.Server",
		"--tls", "true",
		"--database.0", "mem:tlsdb", "--dbname.0", "tlsdb",
		"--port", fmt.Sprintf("%d", port), "--silent", "true")
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start TLS server: %v", err)
	}
	srv := &testServer{
		cmd:  cmd,
		port: port,
		dsn:  fmt.Sprintf("hsqls://SA@localhost:%d/tlsdb", port),
	}
	if err := waitPort(port, 15*time.Second); err != nil {
		_ = cmd.Process.Kill()
		t.Fatalf("TLS server not ready: %v", err)
	}
	t.Cleanup(func() { srv.stop() })
	return srv, certPath
}

func TestTLSConnection(t *testing.T) {
	srv, certPath := startTLSTestServer(t)

	pem, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatalf("read cert: %v", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		t.Fatal("failed to add server cert to pool")
	}
	if err := RegisterTLSConfig("hsqltest", &tls.Config{RootCAs: pool, ServerName: "localhost"}); err != nil {
		t.Fatalf("register tls config: %v", err)
	}
	defer DeregisterTLSConfig("hsqltest")

	db, err := sql.Open("hsql", srv.dsn+"?tlsconfig=hsqltest")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	c := ctx(t)

	if err := db.PingContext(c); err != nil {
		t.Fatalf("ping over TLS: %v", err)
	}
	if _, err := db.ExecContext(c, "CREATE TABLE s (n INTEGER)"); err != nil {
		t.Fatalf("create over TLS: %v", err)
	}
	if _, err := db.ExecContext(c, "INSERT INTO s VALUES (7)"); err != nil {
		t.Fatalf("insert over TLS: %v", err)
	}
	var n int
	if err := db.QueryRowContext(c, "SELECT n FROM s").Scan(&n); err != nil {
		t.Fatalf("query over TLS: %v", err)
	}
	if n != 7 {
		t.Fatalf("got %d, want 7", n)
	}
}

func TestTLSRejectsUntrustedCert(t *testing.T) {
	srv, _ := startTLSTestServer(t)

	// An empty cert pool trusts nothing, so verification must fail.
	if err := RegisterTLSConfig("hsqlstrict", &tls.Config{RootCAs: x509.NewCertPool(), ServerName: "localhost"}); err != nil {
		t.Fatalf("register: %v", err)
	}
	defer DeregisterTLSConfig("hsqlstrict")

	db, err := sql.Open("hsql", srv.dsn+"?tlsconfig=hsqlstrict")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	if err := db.PingContext(ctx(t)); err == nil {
		t.Fatal("expected TLS verification failure against untrusted cert")
	}
}
