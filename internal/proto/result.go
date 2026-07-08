package proto

import "fmt"

// Metadata type codes (org.hsqldb.result.ResultMetaData).
const (
	MetaResult        int32 = 1 // full result-set metadata (types + column info)
	MetaSimpleResult  int32 = 2 // type codes only
	MetaUpdateResult  int32 = 3 // type codes only
	MetaParam         int32 = 4 // parameter metadata (type + label + attrs)
	MetaGeneratedIdx  int32 = 5 // generated-key column indexes
	MetaGeneratedName int32 = 6 // generated-key column names
)

// Column describes one visible result column.
type Column struct {
	Type    ColumnType
	Label   string
	Catalog string
	Schema  string
	Table   string
	Name    string
	Attrs   byte
}

// Metadata is a decoded ResultMetaData. Types holds one entry per extended
// column (used to decode row values); Columns holds the visible columns.
type Metadata struct {
	MetaType      int32
	ColumnCount   int32
	ExtendedCount int32
	Types         []ColumnType // len == ExtendedCount for MetaResult, else ColumnCount
	Columns       []Column     // len == ColumnCount (populated for MetaResult/MetaParam)
	Labels        []string     // for MetaGeneratedName
	ColIndexes    []int32      // for MetaGeneratedIdx, or the extended->visible map
}

// RowSet is a decoded block of result rows.
type RowSet struct {
	ID            int64
	Size          int32 // total rows in the full result
	CurrentOffset int32 // offset of this block within the full result
	Rows          [][]any
}

// Result is the union of fields carried by the Result messages this driver
// sends and receives. Only the fields relevant to a given Mode are meaningful.
type Result struct {
	Mode Mode

	// CONNECT / CONNECTACKNOWLEDGE / ERROR
	DatabaseName    string
	User            string // CONNECT mainString
	Password        string // CONNECT subString
	ZoneString      string
	TimeZoneSeconds int32 // CONNECT updateCount
	DatabaseID      int32
	SessionID       int64
	ClientProps     string // CONNECTACK mainString
	Message         string // ERROR mainString
	SQLState        string // ERROR subString
	ErrorCode       int32

	// statements
	SQL                 string
	StatementID         int64
	StatementReturnType byte
	RSProperties        byte
	GenerateKeys        byte
	QueryTimeout        int16
	FetchSize           int32
	UpdateCount         int32
	ID                  int64 // result-set / cursor id

	// transactions
	TxType        int32
	SavepointName string

	// data
	Meta      *Metadata
	ParamMeta *Metadata
	RowSet    *RowSet

	// EXECUTE parameter values, aligned with ParamMeta.Types
	ParamValues []any

	// Chained holds any additional Results the server sent after the main one
	// in the same transmission (e.g. a GENERATED keys result following an
	// UPDATECOUNT). Populated by the connection layer as it drains the stream.
	Chained []*Result
}

// NewResult returns a Result with the given mode.
func NewResult(mode Mode) *Result { return &Result{Mode: mode} }

// GeneratedKey returns the first generated key value from a chained GENERATED
// result, if present. Used for LastInsertId after an INSERT that requested
// generated keys.
func (r *Result) GeneratedKey() (int64, bool) {
	for _, c := range r.Chained {
		if c.Mode != ModeGenerated || c.RowSet == nil || len(c.RowSet.Rows) == 0 {
			continue
		}
		row := c.RowSet.Rows[0]
		if len(row) == 0 {
			continue
		}
		switch v := row[0].(type) {
		case int64:
			return v, true
		}
	}
	return 0, false
}

// EncodePayload writes the mode-specific payload (everything after the frame's
// mode byte and length field) for a request Result.
func (r *Result) EncodePayload(w *RowOutput) error {
	switch r.Mode {
	case ModeConnect:
		w.WriteString(r.DatabaseName)
		w.WriteString(r.User)
		w.WriteString(r.Password)
		w.WriteString(r.ZoneString)
		w.WriteInt(r.TimeZoneSeconds)
	case ModeDisconnect, ModeResetSession, ModeStartTran:
		// no payload
	case ModeExecDirect:
		w.WriteInt(r.UpdateCount)
		w.WriteInt(r.FetchSize)
		w.WriteU8(r.StatementReturnType)
		w.WriteString(r.SQL)
		w.WriteU8(r.RSProperties)
		w.WriteShort(r.QueryTimeout)
		w.WriteU8(r.GenerateKeys)
	case ModePrepare:
		w.WriteU8(r.StatementReturnType)
		w.WriteString(r.SQL)
		w.WriteU8(r.RSProperties)
		w.WriteU8(r.GenerateKeys)
	case ModeExecute:
		w.WriteInt(r.UpdateCount)
		w.WriteInt(r.FetchSize)
		w.WriteLong(r.StatementID)
		w.WriteU8(r.RSProperties)
		w.WriteShort(r.QueryTimeout)
		if err := r.writeParams(w); err != nil {
			return err
		}
	case ModeEndTran:
		w.WriteInt(r.TxType)
		if r.TxType == TxSavepointNameRelease || r.TxType == TxSavepointNameRollback {
			w.WriteString(r.SavepointName)
		}
	case ModeCloseResult:
		w.WriteLong(r.ID)
	case ModeFreeStmt:
		w.WriteLong(r.StatementID)
	case ModeRequestData:
		w.WriteLong(r.ID)
		w.WriteInt(r.UpdateCount) // row offset to resume from
		w.WriteInt(r.FetchSize)
	default:
		return fmt.Errorf("hsql/proto: cannot encode request mode %d", r.Mode)
	}
	return nil
}

// writeParams encodes EXECUTE parameters via writeSimple: int32(1) then one row
// of values encoded with the parameter metadata's column types.
func (r *Result) writeParams(w *RowOutput) error {
	if r.ParamMeta == nil {
		w.WriteInt(0)
		return nil
	}
	n := int(r.ParamMeta.ColumnCount)
	w.WriteInt(1) // one row of parameters
	for i := 0; i < n; i++ {
		var v any
		if i < len(r.ParamValues) {
			v = r.ParamValues[i]
		}
		if err := w.WriteValue(r.ParamMeta.Types[i], v); err != nil {
			return err
		}
	}
	return nil
}

// DecodeResult parses a response Result from its mode and payload bytes.
func DecodeResult(mode Mode, payload []byte) (*Result, error) {
	r := &Result{Mode: mode}
	in := NewRowInput(payload)
	switch mode {
	case ModeConnectAcknowledge:
		r.DatabaseID = in.ReadInt()
		r.SessionID = in.ReadLong()
		r.DatabaseName = in.ReadString()
		r.ClientProps = in.ReadString()
		_ = in.ReadInt() // trailing generateKeys, unused
	case ModeError, ModeWarning:
		r.Message = in.ReadString()
		r.SQLState = in.ReadString()
		r.ErrorCode = in.ReadInt()
	case ModeUpdateCount:
		r.UpdateCount = in.ReadInt()
	case ModeData, ModeDataHead, ModeGenerated:
		r.ID = in.ReadLong()
		r.UpdateCount = in.ReadInt()
		r.FetchSize = in.ReadInt()
		r.RSProperties = in.ReadU8()
		r.Meta = readMetadata(in)
		r.RowSet = readRowSet(in, r.Meta)
	case ModeDataRows:
		r.Meta = readMetadata(in)
		r.RowSet = readRowSet(in, r.Meta)
	case ModePrepareAck:
		r.StatementReturnType = in.ReadU8()
		r.StatementID = in.ReadLong()
		r.RSProperties = in.ReadU8()
		r.Meta = readMetadata(in)
		r.ParamMeta = readMetadata(in)
	case ModeBatchExecResponse:
		r.UpdateCount = in.ReadInt()
		r.FetchSize = in.ReadInt()
		r.StatementID = in.ReadLong()
		r.QueryTimeout = in.ReadShort()
		r.Meta = readMetadata(in)
		r.RowSet = readRowSetSimple(in, r.Meta)
	case ModeSetSessionAttr, ModeSetConnectAttr, ModeGetSessionAttr:
		// Acknowledgement of a session/connection attribute change (e.g. the
		// server's reply to "SET AUTOCOMMIT ..."). The frame bytes are already
		// consumed by the framing layer; we don't need the payload contents.
	default:
		return nil, fmt.Errorf("hsql/proto: cannot decode response mode %d", mode)
	}
	if err := in.Err(); err != nil {
		return nil, fmt.Errorf("hsql/proto: decoding mode %d: %w", mode, err)
	}
	return r, nil
}

// readMetadata decodes a ResultMetaData following the exact branch structure of
// org.hsqldb.result.ResultMetaData.
func readMetadata(in *RowInput) *Metadata {
	m := &Metadata{}
	m.MetaType = in.ReadInt()
	m.ColumnCount = in.ReadInt()
	if in.Err() != nil {
		return m
	}
	switch m.MetaType {
	case MetaSimpleResult, MetaUpdateResult:
		m.Types = make([]ColumnType, m.ColumnCount)
		for i := range m.Types {
			m.Types[i] = in.ReadDataTypeSimple()
		}
	case MetaGeneratedIdx:
		m.ColIndexes = make([]int32, m.ColumnCount)
		for i := range m.ColIndexes {
			m.ColIndexes[i] = in.ReadInt()
		}
	case MetaGeneratedName:
		m.Labels = make([]string, m.ColumnCount)
		for i := range m.Labels {
			m.Labels[i] = in.ReadString()
		}
	case MetaParam:
		m.Types = make([]ColumnType, m.ColumnCount)
		m.Columns = make([]Column, m.ColumnCount)
		for i := range m.Types {
			m.Types[i] = in.ReadDataType()
			label := in.ReadString()
			attrs := in.ReadU8()
			m.Columns[i] = Column{Type: m.Types[i], Label: label, Attrs: attrs}
		}
	case MetaResult:
		m.ExtendedCount = in.ReadInt()
		m.Types = make([]ColumnType, m.ExtendedCount)
		for i := range m.Types {
			m.Types[i] = in.ReadDataType()
		}
		m.Columns = make([]Column, m.ColumnCount)
		for i := range m.Columns {
			col := Column{}
			col.Label = in.ReadString()
			col.Catalog = in.ReadString()
			col.Schema = in.ReadString()
			col.Table = in.ReadString()
			col.Name = in.ReadString()
			col.Attrs = in.ReadU8()
			col.Type = m.Types[i]
			m.Columns[i] = col
		}
		if m.ColumnCount != m.ExtendedCount {
			m.ColIndexes = make([]int32, m.ColumnCount)
			for i := range m.ColIndexes {
				m.ColIndexes[i] = in.ReadInt()
			}
		}
	default:
		in.err = fmt.Errorf("hsql/proto: unknown metadata type %d", m.MetaType)
	}
	return m
}

// rowTypes returns the per-value type slice used to decode a row. For
// MetaResult the row carries ExtendedCount values; otherwise ColumnCount.
func (m *Metadata) rowTypes() []ColumnType { return m.Types }

// readRowSet decodes a full result-set block (RowSetNavigatorClient.read):
// id, size, currentOffset, baseBlockSize, then baseBlockSize rows.
func readRowSet(in *RowInput, m *Metadata) *RowSet {
	rs := &RowSet{}
	rs.ID = in.ReadLong()
	rs.Size = in.ReadInt()
	rs.CurrentOffset = in.ReadInt()
	blockLen := in.ReadInt()
	if in.Err() != nil || blockLen < 0 {
		return rs
	}
	types := m.rowTypes()
	rs.Rows = make([][]any, 0, blockLen)
	for i := int32(0); i < blockLen; i++ {
		row := make([]any, len(types))
		for j := range types {
			row[j] = in.ReadValue(types[j])
		}
		rs.Rows = append(rs.Rows, row)
		if in.Err() != nil {
			break
		}
	}
	return rs
}

// readRowSetSimple decodes a RowSetNavigatorClient.readSimple block: an int32
// count followed by that many rows using the visible column types.
func readRowSetSimple(in *RowInput, m *Metadata) *RowSet {
	rs := &RowSet{}
	n := in.ReadInt()
	if in.Err() != nil || n < 0 {
		return rs
	}
	types := m.rowTypes()
	rs.Rows = make([][]any, 0, n)
	for i := int32(0); i < n; i++ {
		row := make([]any, len(types))
		for j := range types {
			row[j] = in.ReadValue(types[j])
		}
		rs.Rows = append(rs.Rows, row)
		if in.Err() != nil {
			break
		}
	}
	return rs
}
