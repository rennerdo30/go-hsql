package hsql

import (
	"crypto/tls"
	"testing"
)

func TestParseDSN(t *testing.T) {
	t.Run("full", func(t *testing.T) {
		cfg, err := ParseDSN("hsql://alice:secret@db.example.com:9005/prod?zone=Europe/Berlin&tzoffset=3600&fetchsize=50")
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Host != "db.example.com" || cfg.Port != 9005 {
			t.Errorf("host/port = %s:%d", cfg.Host, cfg.Port)
		}
		if cfg.User != "alice" || cfg.Password != "secret" {
			t.Errorf("user/pass = %s/%s", cfg.User, cfg.Password)
		}
		if cfg.Database != "prod" {
			t.Errorf("db = %s", cfg.Database)
		}
		if cfg.TLS {
			t.Error("TLS should be false for hsql://")
		}
		if cfg.Zone != "Europe/Berlin" || cfg.TimeZoneSeconds != 3600 || cfg.FetchSize != 50 {
			t.Errorf("params = %+v", cfg)
		}
	})

	t.Run("defaults", func(t *testing.T) {
		cfg, err := ParseDSN("hsql:///mydb")
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Host != "localhost" || cfg.Port != 9001 {
			t.Errorf("defaults host/port = %s:%d", cfg.Host, cfg.Port)
		}
		if cfg.User != "SA" {
			t.Errorf("default user = %q, want SA", cfg.User)
		}
		if cfg.Zone != "UTC" {
			t.Errorf("default zone = %q", cfg.Zone)
		}
	})

	t.Run("tls", func(t *testing.T) {
		cfg, err := ParseDSN("hsqls://host/db")
		if err != nil || !cfg.TLS {
			t.Fatalf("hsqls should set TLS: %+v %v", cfg, err)
		}
	})

	t.Run("tls config", func(t *testing.T) {
		if err := RegisterTLSConfig("test", &tls.Config{ServerName: "db.example.com"}); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { DeregisterTLSConfig("test") })
		cfg, err := ParseDSN("hsqls://host/db?tlsconfig=test")
		if err != nil {
			t.Fatal(err)
		}
		if cfg.TLSConfigName != "test" || cfg.TLSConfig == nil || cfg.TLSConfig.ServerName != "db.example.com" {
			t.Fatalf("tls config not applied: %+v", cfg)
		}
		cfg.TLSConfig.ServerName = "mutated"
		cfg2, err := ParseDSN("hsqls://host/db?tlsconfig=test")
		if err != nil {
			t.Fatal(err)
		}
		if cfg2.TLSConfig.ServerName != "db.example.com" {
			t.Fatalf("tls config lookup should clone registered config, got %q", cfg2.TLSConfig.ServerName)
		}
	})

	t.Run("errors", func(t *testing.T) {
		bad := []string{
			"mysql://host/db", // wrong scheme
			"hsql://host",     // missing db alias
			"hsql://host/db?tzoffset=nope",
			"hsql://host/db?fetchsize=-1",
			"hsql://host/db?tlsconfig=test",
			"hsqls://host/db?tlsconfig=missing",
			"hsql://host:notaport/db",
		}
		for _, dsn := range bad {
			if _, err := ParseDSN(dsn); err == nil {
				t.Errorf("expected error for %q", dsn)
			}
		}
	})
}
