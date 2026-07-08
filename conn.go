package hsql

import (
	"bufio"
	"context"
	"crypto/tls"
	"database/sql/driver"
	"fmt"
	"net"
	"time"

	"github.com/rennerdo30/go-hsql/internal/proto"
)

// conn is a single connection to an HSQLDB server.
type conn struct {
	cfg     *Config
	netConn net.Conn
	bw      *bufio.Writer
	br      *bufio.Reader
	rowOut  *proto.RowOutput

	sessionID  int64
	databaseID int32
	lobIDSeq   int64

	closed     bool
	broken     bool // an I/O error or cancellation left the wire desynced
	autocommit bool // true when not inside an explicit transaction
}

var (
	_ driver.Conn               = (*conn)(nil)
	_ driver.ConnPrepareContext = (*conn)(nil)
	_ driver.ExecerContext      = (*conn)(nil)
	_ driver.QueryerContext     = (*conn)(nil)
	_ driver.ConnBeginTx        = (*conn)(nil)
	_ driver.Pinger             = (*conn)(nil)
	_ driver.SessionResetter    = (*conn)(nil)
	_ driver.Validator          = (*conn)(nil)
	_ driver.NamedValueChecker  = (*conn)(nil)
)

// connect dials the server and performs the handshake + login.
func connect(ctx context.Context, cfg *Config) (*conn, error) {
	d := net.Dialer{}
	var netConn net.Conn
	var err error
	if cfg.TLS {
		netConn, err = (&tls.Dialer{NetDialer: &d}).DialContext(ctx, "tcp", cfg.address())
	} else {
		netConn, err = d.DialContext(ctx, "tcp", cfg.address())
	}
	if err != nil {
		return nil, fmt.Errorf("hsql: dial %s: %w", cfg.address(), err)
	}
	if tc, ok := netConn.(*net.TCPConn); ok {
		_ = tc.SetNoDelay(true)
	}

	c := &conn{
		cfg:        cfg,
		netConn:    netConn,
		bw:         bufio.NewWriter(netConn),
		br:         bufio.NewReader(netConn),
		rowOut:     proto.NewRowOutput(),
		lobIDSeq:   -1,
		autocommit: true,
	}

	if err := c.handshake(ctx); err != nil {
		_ = netConn.Close()
		return nil, err
	}
	return c, nil
}

// handshake writes the compatibility version int, then performs the CONNECT /
// CONNECTACKNOWLEDGE exchange.
func (c *conn) handshake(ctx context.Context) error {
	stop := c.watchCancel(ctx)
	defer stop()

	// 1. Version int (raw 4-byte big-endian, no frame).
	v := int32(proto.NetworkCompatibilityVersionInt)
	if _, err := c.bw.Write([]byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)}); err != nil {
		return fmt.Errorf("hsql: writing handshake: %w", err)
	}
	if err := c.bw.Flush(); err != nil {
		return err
	}

	// 2. CONNECT request.
	req := proto.NewResult(proto.ModeConnect)
	req.DatabaseName = c.cfg.Database
	req.User = c.cfg.User
	req.Password = c.cfg.Password
	req.ZoneString = c.cfg.Zone
	req.TimeZoneSeconds = int32(c.cfg.TimeZoneSeconds)

	res, err := c.exchange(req)
	if err != nil {
		return err
	}
	if res.Mode != proto.ModeConnectAcknowledge {
		return fmt.Errorf("hsql: unexpected handshake response mode %d", res.Mode)
	}
	c.sessionID = res.SessionID
	c.databaseID = res.DatabaseID
	return nil
}

// send serializes a request Result as a framed transmission and flushes it.
func (c *conn) send(req *proto.Result) error {
	if err := c.prepareArrayParams(req); err != nil {
		return err
	}
	lobs, err := c.prepareLobParams(req)
	if err != nil {
		return err
	}
	c.rowOut.Reset()
	if err := req.EncodePayload(c.rowOut); err != nil {
		return err
	}
	if err := proto.WriteFrame(c.bw, req.Mode, c.rowOut.Bytes()); err != nil {
		return err
	}
	for _, lob := range lobs {
		if err := c.writeLobCreate(lob); err != nil {
			c.broken = true
			return err
		}
	}
	if err := proto.WriteTerminator(c.bw); err != nil {
		return err
	}
	return c.bw.Flush()
}

// receive reads a full response transmission: the main Result frame followed by
// any chained frames up to the NONE terminator. Chained frames are decoded to
// keep the stream aligned but only the main Result is returned.
func (c *conn) receive() (*proto.Result, error) {
	mode, payload, err := proto.ReadFrame(c.br)
	if err != nil {
		return nil, fmt.Errorf("hsql: reading response: %w", err)
	}
	main, err := proto.DecodeResult(mode, payload)
	if err != nil {
		return nil, err
	}
	for {
		m2, p2, err := proto.ReadFrame(c.br)
		if err != nil {
			return nil, fmt.Errorf("hsql: reading chained response: %w", err)
		}
		if m2 == proto.ModeNone {
			break
		}
		chained, err := proto.DecodeResult(m2, p2)
		if err != nil {
			return nil, err
		}
		main.Chained = append(main.Chained, chained)
	}
	return main, nil
}

// exec runs a request with a background context (internal, non-cancelable
// callers such as result-set paging and Close).
func (c *conn) exec(req *proto.Result) (*proto.Result, error) {
	return c.execCtx(context.Background(), req)
}

// execCtx runs a request honoring ctx cancellation/deadline. If the context is
// canceled mid-exchange the connection is left in an unknown protocol state, so
// it is marked broken and dropped from the pool.
func (c *conn) execCtx(ctx context.Context, req *proto.Result) (*proto.Result, error) {
	if c.closed || c.broken {
		return nil, driver.ErrBadConn
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	stop := c.watchCancel(ctx)
	defer stop()

	res, err := c.exchange(req)
	if err != nil {
		c.broken = true
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, err
	}
	if res.Mode == proto.ModeError {
		return res, errorFromResult(res)
	}
	return res, nil
}

// exchange performs the raw send/receive round-trip with no context handling.
func (c *conn) exchange(req *proto.Result) (*proto.Result, error) {
	if err := c.send(req); err != nil {
		return nil, err
	}
	return c.receive()
}

// watchCancel wires ctx cancellation to the socket: if ctx has a deadline it is
// applied to the connection, and a watcher goroutine forces an immediate
// deadline on cancellation to unblock any in-flight read/write. The returned
// stop function tears down the watcher and clears the deadline.
func (c *conn) watchCancel(ctx context.Context) func() {
	if ctx.Done() == nil {
		return func() {}
	}
	if dl, ok := ctx.Deadline(); ok {
		_ = c.netConn.SetDeadline(dl)
	}
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = c.netConn.SetDeadline(time.Now())
		case <-done:
		}
	}()
	return func() {
		close(done)
		if !c.broken {
			_ = c.netConn.SetDeadline(time.Time{})
		}
	}
}

// Ping implements driver.Pinger with a lightweight round-trip.
func (c *conn) Ping(ctx context.Context) error {
	if c.closed || c.broken {
		return driver.ErrBadConn
	}
	_, err := c.execCtx(ctx, c.newExecDirect("VALUES(1)"))
	return err
}

// Prepare implements driver.Conn.
func (c *conn) Prepare(query string) (driver.Stmt, error) {
	return c.PrepareContext(context.Background(), query)
}

// CheckNamedValue lets database/sql pass driver-specific streaming LOB
// parameters through for implicit prepared-statement execution.
func (c *conn) CheckNamedValue(nv *driver.NamedValue) error {
	return checkNamedValue(nv)
}

// Close closes the connection, attempting a graceful DISCONNECT first.
func (c *conn) Close() error {
	if c.closed {
		return nil
	}
	c.closed = true
	// Best-effort graceful disconnect; ignore errors since we're closing.
	_ = c.send(proto.NewResult(proto.ModeDisconnect))
	return c.netConn.Close()
}

// IsValid implements driver.Validator for connection-pool health checks. A
// connection that hit an I/O error or a canceled operation is no longer safe to
// reuse because the wire may be desynced.
func (c *conn) IsValid() bool { return !c.closed && !c.broken }

// ResetSession implements driver.SessionResetter.
func (c *conn) ResetSession(ctx context.Context) error {
	if c.closed || c.broken {
		return driver.ErrBadConn
	}
	// HSQLDB's native RESETSESSION also clears the server-side prepared
	// statement manager, which breaks database/sql statements that survive pool
	// reuse. Use SQL cleanup that preserves prepared statement ids.
	if _, err := c.execCtx(ctx, c.newExecDirect("ROLLBACK")); err != nil {
		return err
	}
	if _, err := c.execCtx(ctx, c.newExecDirect("SET AUTOCOMMIT TRUE")); err != nil {
		return err
	}
	c.autocommit = true
	c.lobIDSeq = -1
	return nil
}
