// Package hsql is a pure-Go database/sql driver for HSQLDB (HyperSQL), speaking
// the native binary network protocol over TCP (hsql:// and hsqls://).
//
// Register is automatic on import; open a connection with:
//
//	import _ "github.com/rennerdo30/go-hsql"
//	db, err := sql.Open("hsql", "hsql://SA@localhost:9001/mydb")
package hsql

import (
	"context"
	"database/sql"
	"database/sql/driver"
)

func init() {
	sql.Register("hsql", &Driver{})
}

// Driver implements driver.Driver and driver.DriverContext.
type Driver struct{}

var (
	_ driver.Driver        = (*Driver)(nil)
	_ driver.DriverContext = (*Driver)(nil)
)

// Open implements driver.Driver.
func (d *Driver) Open(dsn string) (driver.Conn, error) {
	c, err := d.OpenConnector(dsn)
	if err != nil {
		return nil, err
	}
	return c.Connect(context.Background())
}

// OpenConnector implements driver.DriverContext.
func (d *Driver) OpenConnector(dsn string) (driver.Connector, error) {
	cfg, err := ParseDSN(dsn)
	if err != nil {
		return nil, err
	}
	return &connector{cfg: cfg}, nil
}

// connector implements driver.Connector.
type connector struct {
	cfg *Config
}

var _ driver.Connector = (*connector)(nil)

// Connect opens a new connection to the server.
func (c *connector) Connect(ctx context.Context) (driver.Conn, error) {
	return connect(ctx, c.cfg)
}

// Driver returns the underlying driver.
func (c *connector) Driver() driver.Driver { return &Driver{} }
