// Package proto implements the HSQLDB (HyperSQL) native network wire protocol.
//
// The protocol is a big-endian binary framing over TCP (default port 9001).
// Every message ("Result") is serialized as:
//
//	[mode: 1 byte][length: int32][payload: length-4 bytes]
//
// where length counts its own 4 bytes but not the leading mode byte. A single
// transmission is one or more chained Result frames terminated by a lone 0x00
// (mode NONE) byte.
//
// The constants and encodings here are derived from the HSQLDB Java source
// (org.hsqldb: ClientConnection, ServerConnection, result.*, rowio.*, types.Types).
package proto

// NetworkCompatibilityVersionInt is the first int32 the client writes on the
// wire during the handshake. It must byte-match the server's
// org.hsqldb.ClientConnection.NETWORK_COMPATIBILITY_VERSION_INT or the server
// rejects the connection with SERVER_VERSIONS_INCOMPATIBLE.
//
// The value encodes the dotted version "2.3.4.0": each segment multiplied by
// 100^position (counting from the right, 0-based), summed, then negated. It has
// been stable across the entire HSQLDB 2.x line (2.3.4 through 2.7.x).
const NetworkCompatibilityVersionInt = -2030400

// DefaultPort is the default TCP port for the hsql:// protocol.
const DefaultPort = 9001

// Mode is a Result type code (org.hsqldb.result.ResultConstants). It occupies
// the leading byte of every frame.
type Mode byte

// Result mode constants (org.hsqldb.result.ResultConstants).
const (
	ModeNone               Mode = 0  // frame-chain terminator
	ModeUpdateCount        Mode = 1  // rows-affected response
	ModeError              Mode = 2  // error response
	ModeData               Mode = 3  // result-set response
	ModePrepareAck         Mode = 4  // prepared-statement ack
	ModeSQLCancel          Mode = 5  // cancel running statement
	ModeSetSessionAttr     Mode = 6  // set session attribute
	ModeGetSessionAttr     Mode = 7  // get session attribute
	ModeBatchExecDirect    Mode = 8  // batch of direct SQL
	ModeBatchExecute       Mode = 9  // batch of prepared executions
	ModeResetSession       Mode = 10 // reset session state
	ModeConnectAcknowledge Mode = 11 // connect success response
	ModePrepareCommit      Mode = 12 // prepare-to-commit (2PC)
	ModeRequestData        Mode = 13 // request another result-set block
	ModeDataRows           Mode = 14 // additional result-set block
	ModeDataHead           Mode = 15 // result-set head
	ModeBatchExecResponse  Mode = 16 // batch response with per-stmt counts
	ModeParamMetadata      Mode = 17 // parameter metadata
	ModeLargeObjectOp      Mode = 18 // LOB operation
	ModeWarning            Mode = 19 // warning response
	ModeGenerated          Mode = 20 // generated keys response
	ModeExecuteInvalid     Mode = 21 // prepared statement no longer valid
	ModeConnect            Mode = 31 // connect / login request
	ModeDisconnect         Mode = 32 // disconnect request
	ModeEndTran            Mode = 33 // commit / rollback / savepoint
	ModeExecDirect         Mode = 34 // execute direct SQL
	ModeExecute            Mode = 35 // execute prepared statement
	ModeFreeStmt           Mode = 36 // free a prepared statement
	ModePrepare            Mode = 37 // prepare a statement
	ModeSetConnectAttr     Mode = 38 // set connection attribute
	ModeStartTran          Mode = 39 // begin transaction
	ModeCloseResult        Mode = 40 // close a server-side result cursor
	ModeUpdateResult       Mode = 41 // update via result set
	ModeValue              Mode = 42 // scalar value
	ModeCallResponse       Mode = 43 // stored-procedure response
	ModeChangeSet          Mode = 44 // change set
	ModeUpperLimit         Mode = 48 // values >= this are ODBC, not HSQL
)

// ENDTRAN sub-codes (the transaction-type int in an ENDTRAN request).
const (
	TxCommit                = 0
	TxRollback              = 1
	TxSavepointNameRollback = 2
	TxSavepointNameRelease  = 4
	TxCommitAndChain        = 6
	TxRollbackAndChain      = 7
	TxPrepareCommit         = 12 // PREPARECOMMIT: first phase of a 2PC
)

// SETCONNECTATTR attribute codes.
const (
	ConnectAttrSavepointName int32 = 10027
)

// UPDATE_RESULT cursor action types (org.hsqldb.StatementTypes, carried as the
// action int in an UPDATE_RESULT request).
const (
	ActionUpdateCursor int32 = 91 // StatementTypes.UPDATE_CURSOR
	ActionDeleteCursor int32 = 18 // StatementTypes.DELETE_CURSOR
	ActionInsertCursor int32 = 55 // StatementTypes.INSERT
)

// Result-set property bits (org.hsqldb.result.ResultProperties). rsProperties
// is a bit-packed byte sent with PREPARE/EXECDIRECT and echoed back by the
// server with the effective capabilities of the result.
const (
	RSReturnable byte = 1 << 0
	RSHoldable   byte = 1 << 1
	RSScrollable byte = 1 << 2
	RSUpdatable  byte = 1 << 3
	RSSensitive  byte = 1 << 4
	RSIsHeld     byte = 1 << 5
)

// Parameter modes (org.hsqldb.SchemaObject.ParameterModes), carried in the high
// nibble of a PARAM_METADATA column's attrs byte.
const (
	ParamModeUnknown byte = 0
	ParamModeIn      byte = 1
	ParamModeInOut   byte = 2
	ParamModeOut     byte = 4
)

// Updatable-result system column offsets relative to the visible column count
// (org.hsqldb.result.ResultMetaData.SysOffsets). An updatable result's extended
// row carries these extra columns after the visible ones.
const (
	SysOffsetRowID     = 0
	SysOffsetRowStatus = 1
	SysOffsetRowNum    = 2
)

// Session attribute ids (org.hsqldb.SessionInterface.Attributes), used as the
// INFO_ID value in a SETSESSIONATTR row.
const (
	AttrIsolation          int32 = 0
	AttrAutocommit         int32 = 1
	AttrConnectionReadonly int32 = 2
	AttrCatalog            int32 = 3
	AttrTimezone           int32 = 4
)

// Session-attribute row column count (AttributePos.INFO_LIMIT).
const attrPosLimit = 4

// Transaction isolation levels on the wire (org.hsqldb.SessionInterface).
const (
	TxReadUncommitted int32 = 1
	TxReadCommitted   int32 = 2
	TxRepeatableRead  int32 = 4
	TxSerializable    int32 = 8
)

// Statement return types (org.hsqldb.StatementTypes). Written as the
// statementReturnType byte in EXECDIRECT / PREPARE.
const (
	// StatementReturnAny lets the server return either a result set or an
	// update count, chosen from the statement (RETURN_ANY).
	StatementReturnAny byte = 0
	// StatementReturnCount requests an update count (RETURN_COUNT).
	StatementReturnCount byte = 1
	// StatementReturnResult requests a result set (RETURN_RESULT).
	StatementReturnResult byte = 2
)

// Generated-keys request modes (org.hsqldb.result.ResultConstants).
const (
	ReturnGeneratedKeys         byte = 1
	ReturnNoGeneratedKeys       byte = 2
	ReturnGeneratedKeysColNames byte = 11
	ReturnGeneratedKeysColIdx   byte = 21
	ReturnPrimaryKeys           byte = 31
)

// Batch per-statement update-count sentinels.
const (
	BatchExecuteFailed = -3
	BatchSuccessNoInfo = -2
)

// LOB operation sub-types (org.hsqldb.result.ResultLob.LobResultTypes). These
// are carried in a LARGE_OBJECT_OP (mode 18) frame, which uses its own framing
// (no length prefix): mode, int32 databaseID, int64 sessionID, int64 lobID,
// int32 subType, then sub-type-specific fields, then a NONE terminator byte.
const (
	LobReqGetBytes    int32 = 1
	LobReqSetBytes    int32 = 2
	LobReqGetChars    int32 = 3
	LobReqSetChars    int32 = 4
	LobReqCreateBytes int32 = 7
	LobReqCreateChars int32 = 8
	LobReqGetLength   int32 = 10
	LobReqGetLob      int32 = 11

	LobRespGetBytes    int32 = 21
	LobRespSet         int32 = 22
	LobRespGetChars    int32 = 23
	LobRespCreateBytes int32 = 27
	LobRespCreateChars int32 = 28
	LobRespTruncate    int32 = 29
)

// LobRef is returned by ReadValue for a CLOB/BLOB column. It carries the
// server-side LOB id; the driver resolves it to bytes/string via the LOB
// sub-protocol. IsClob distinguishes character LOBs (UTF-16 chars) from binary.
type LobRef struct {
	ID     int64
	IsClob bool
}

// ResultSet property defaults. rsProperties is a bit-packed byte; 0 corresponds
// to the common forward-only / read-only / hold-cursors-over-commit case that
// the reference client uses for ordinary statements.
const DefaultRSProperties byte = 0

// TypeCode is an org.hsqldb.types.Types SQL type code, serialized on the wire
// as a 2-byte short inside result/parameter metadata.
type TypeCode int16

// SQL type codes (org.hsqldb.types.Types).
const (
	SQLAllTypes               TypeCode = 0
	SQLChar                   TypeCode = 1
	SQLNumeric                TypeCode = 2
	SQLDecimal                TypeCode = 3
	SQLInteger                TypeCode = 4
	SQLSmallint               TypeCode = 5
	SQLFloat                  TypeCode = 6
	SQLReal                   TypeCode = 7
	SQLDouble                 TypeCode = 8
	SQLVarchar                TypeCode = 12
	SQLBit                    TypeCode = 14
	SQLBitVarying             TypeCode = 15
	SQLBoolean                TypeCode = 16
	SQLBigint                 TypeCode = 25
	SQLBlob                   TypeCode = 30
	SQLClob                   TypeCode = 40
	SQLBinary                 TypeCode = 60
	SQLVarbinary              TypeCode = 61
	SQLDate                   TypeCode = 91
	SQLTime                   TypeCode = 92
	SQLTimestamp              TypeCode = 93
	SQLTimeTZ                 TypeCode = 94
	SQLTimestampTZ            TypeCode = 95
	SQLIntervalYear           TypeCode = 101
	SQLIntervalMonth          TypeCode = 102
	SQLIntervalDay            TypeCode = 103
	SQLIntervalHour           TypeCode = 104
	SQLIntervalMinute         TypeCode = 105
	SQLIntervalSecond         TypeCode = 106
	SQLIntervalYearToMonth    TypeCode = 107
	SQLIntervalDayToHour      TypeCode = 108
	SQLIntervalDayToMinute    TypeCode = 109
	SQLIntervalDayToSecond    TypeCode = 110
	SQLIntervalHourToMinute   TypeCode = 111
	SQLIntervalHourToSecond   TypeCode = 112
	SQLIntervalMinuteToSecond TypeCode = 113
	SQLTinyint                TypeCode = -6
	SQLGuid                   TypeCode = -11
	SQLArray                  TypeCode = 50
	SQLOther                  TypeCode = 1111
)
