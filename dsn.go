package hsql

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/rennerdo30/go-hsql/internal/proto"
)

// Config holds a parsed HSQLDB connection string.
type Config struct {
	Host            string
	Port            int
	User            string
	Password        string
	Database        string // server-side database alias (not a file path)
	TLS             bool
	Zone            string // session time zone id sent in CONNECT
	TimeZoneSeconds int    // offset from UTC in seconds sent in CONNECT
	FetchSize       int    // rows per result-set block (0 = server default)
}

// ParseDSN parses a connection string of the form:
//
//	hsql://[user[:password]@]host[:port]/dbAlias[?param=value&...]
//	hsqls://...   (same, over TLS)
//
// Recognized query parameters: zone (session time-zone id), tzoffset (seconds
// from UTC), fetchsize (rows per block).
func ParseDSN(dsn string) (*Config, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return nil, fmt.Errorf("hsql: invalid DSN: %w", err)
	}
	cfg := &Config{
		Zone:      "UTC",
		FetchSize: 0,
	}
	switch strings.ToLower(u.Scheme) {
	case "hsql":
		cfg.TLS = false
	case "hsqls":
		cfg.TLS = true
	default:
		return nil, fmt.Errorf("hsql: unsupported scheme %q (want hsql:// or hsqls://)", u.Scheme)
	}

	host, port := u.Hostname(), u.Port()
	if host == "" {
		host = "localhost"
	}
	cfg.Host = host
	if port == "" {
		cfg.Port = proto.DefaultPort
	} else {
		p, err := strconv.Atoi(port)
		if err != nil {
			return nil, fmt.Errorf("hsql: invalid port %q: %w", port, err)
		}
		cfg.Port = p
	}

	if u.User != nil {
		cfg.User = u.User.Username()
		if pw, ok := u.User.Password(); ok {
			cfg.Password = pw
		}
	}
	if cfg.User == "" {
		cfg.User = "SA" // HSQLDB default administrative user
	}

	cfg.Database = strings.TrimPrefix(u.Path, "/")
	if cfg.Database == "" {
		return nil, fmt.Errorf("hsql: DSN missing database alias (e.g. hsql://host/mydb)")
	}

	q := u.Query()
	if v := q.Get("zone"); v != "" {
		cfg.Zone = v
	}
	if v := q.Get("tzoffset"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return nil, fmt.Errorf("hsql: invalid tzoffset %q: %w", v, err)
		}
		cfg.TimeZoneSeconds = n
	}
	if v := q.Get("fetchsize"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return nil, fmt.Errorf("hsql: invalid fetchsize %q", v)
		}
		cfg.FetchSize = n
	}
	return cfg, nil
}

// address returns the host:port dial target.
func (c *Config) address() string {
	return net.JoinHostPort(c.Host, strconv.Itoa(c.Port))
}
