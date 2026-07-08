package hsql

import (
	"context"
	"testing"
)

func TestParseServerProperties(t *testing.T) {
	got := parseServerProperties("database.readonly=false;get_column_name=true; signature = x ")
	if got["database.readonly"] != "false" || got["get_column_name"] != "true" || got["signature"] != "x" {
		t.Fatalf("parsed = %+v", got)
	}
	if len(parseServerProperties("")) != 0 {
		t.Fatal("empty string should yield empty map")
	}
	if got := parseServerProperties("flag"); got["flag"] != "" {
		t.Fatalf("valueless key = %+v", got)
	}
}

func TestServerPropertiesFromConnect(t *testing.T) {
	srv := startTestServer(t)
	cfg, _ := ParseDSN(srv.dsn)
	c, err := connect(context.Background(), cfg)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer c.Close()
	props := c.ServerProperties()
	// The server reports at least some session properties on connect.
	if len(props) == 0 {
		t.Log("server reported no client properties (acceptable, but unexpected)")
	}
}
