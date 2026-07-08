package hsql

import (
	"bufio"
	"crypto/tls"
	"net"
	"time"

	"github.com/rennerdo30/go-hsql/internal/proto"
)

// cancelTimeout bounds the best-effort side-channel cancel round-trip.
const cancelTimeout = 5 * time.Second

// fireCancel sends an SQLCANCEL on a fresh, short-lived connection asking the
// server to abort the statement currently running on this session. It mirrors
// the reference Java client, which opens a second connection carrying the
// original session id and random-id token. Best effort: any error is ignored.
func (c *conn) fireCancel() {
	if c.sessionID == 0 {
		return
	}
	d := net.Dialer{Timeout: cancelTimeout}
	var nc net.Conn
	var err error
	if c.cfg.TLS {
		tlsCfg := c.cfg.TLSConfig
		if tlsCfg != nil {
			tlsCfg = tlsCfg.Clone()
		} else {
			tlsCfg = &tls.Config{}
		}
		if tlsCfg.ServerName == "" {
			tlsCfg.ServerName = c.cfg.Host
		}
		nc, err = tls.DialWithDialer(&d, "tcp", c.cfg.address(), tlsCfg)
	} else {
		nc, err = d.Dial("tcp", c.cfg.address())
	}
	if err != nil {
		return
	}
	defer nc.Close()
	_ = nc.SetDeadline(time.Now().Add(cancelTimeout))

	bw := bufio.NewWriter(nc)
	// Handshake: raw compatibility-version int.
	v := int32(proto.NetworkCompatibilityVersionInt)
	if _, err := bw.Write([]byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)}); err != nil {
		return
	}

	req := proto.NewResult(proto.ModeSQLCancel)
	req.DatabaseID = c.databaseID
	req.SessionID = c.sessionID
	req.StatementID = 0 // cancel whatever statement is running on the session
	req.RandomID = c.randomID

	out := proto.NewRowOutput()
	if err := req.EncodePayload(out); err != nil {
		return
	}
	if err := proto.WriteFrame(bw, req.Mode, out.Bytes()); err != nil {
		return
	}
	if err := proto.WriteTerminator(bw); err != nil {
		return
	}
	if err := bw.Flush(); err != nil {
		return
	}
	// Best-effort: drain the response so the server completes cleanly.
	br := bufio.NewReader(nc)
	_, _, _ = proto.ReadFrame(br)
}
