package hsql

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"

	"github.com/rennerdo30/go-hsql/internal/proto"
)

// SessionInfo is a snapshot of server-side session state, fetched over the
// native GETSESSIONATTR protocol (SessionInterface.getAttribute parity).
// Intended for use through database/sql Conn.Raw.
type SessionInfo struct {
	Isolation  sql.IsolationLevel
	AutoCommit bool
	ReadOnly   bool
	Catalog    string
	TimeZone   string
}

// Positions of the session attribute row columns
// (org.hsqldb.SessionInterface.AttributePos).
const (
	attrPosID      = 0
	attrPosInteger = 1
	attrPosBoolean = 2
	attrPosVarchar = 3
)

// SessionInfo fetches the server's view of this session: isolation level,
// autocommit, read-only state, catalog and time zone. Intended for use through
// database/sql Conn.Raw.
func (c *conn) SessionInfo(ctx context.Context) (*SessionInfo, error) {
	info := &SessionInfo{}

	iso, err := c.sessionAttrRow(ctx, proto.AttrIsolation)
	if err != nil {
		return nil, err
	}
	if v, ok := iso[attrPosInteger].(int64); ok {
		info.Isolation = isolationFromWire(int32(v))
	}

	ac, err := c.sessionAttrRow(ctx, proto.AttrAutocommit)
	if err != nil {
		return nil, err
	}
	info.AutoCommit, _ = ac[attrPosBoolean].(bool)

	ro, err := c.sessionAttrRow(ctx, proto.AttrConnectionReadonly)
	if err != nil {
		return nil, err
	}
	info.ReadOnly, _ = ro[attrPosBoolean].(bool)

	cat, err := c.sessionAttrRow(ctx, proto.AttrCatalog)
	if err != nil {
		return nil, err
	}
	info.Catalog, _ = cat[attrPosVarchar].(string)

	tz, err := c.sessionAttrRow(ctx, proto.AttrTimezone)
	if err != nil {
		return nil, err
	}
	info.TimeZone, _ = tz[attrPosVarchar].(string)

	return info, nil
}

// sessionAttrRow issues one GETSESSIONATTR request and returns the attribute
// row ([id, integer, boolean, varchar]).
func (c *conn) sessionAttrRow(ctx context.Context, attrID int32) ([]any, error) {
	if c.closed || c.broken {
		return nil, driver.ErrBadConn
	}
	req := proto.NewResult(proto.ModeGetSessionAttr)
	req.StatementReturnType = byte(attrID)
	res, err := c.execCtx(ctx, req)
	if err != nil {
		return nil, err
	}
	if res.RowSet == nil || len(res.RowSet.Rows) == 0 || len(res.RowSet.Rows[0]) <= attrPosVarchar {
		return nil, fmt.Errorf("hsql: malformed session attribute response (mode %d)", res.Mode)
	}
	return res.RowSet.Rows[0], nil
}

// isolationFromWire maps HSQLDB wire isolation codes to sql.IsolationLevel.
func isolationFromWire(v int32) sql.IsolationLevel {
	switch v {
	case proto.TxReadUncommitted:
		return sql.LevelReadUncommitted
	case proto.TxReadCommitted:
		return sql.LevelReadCommitted
	case proto.TxRepeatableRead:
		return sql.LevelRepeatableRead
	case proto.TxSerializable:
		return sql.LevelSerializable
	default:
		return sql.LevelDefault
	}
}
