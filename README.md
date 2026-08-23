# go-hsql

A pure-Go [`database/sql`](https://pkg.go.dev/database/sql) driver for
**HSQLDB / HyperSQL**, speaking the native HSQLDB binary network protocol over
TCP (`hsql://` and `hsqls://`). No JDBC, no CGo, no external dependencies — it
talks the wire protocol directly.

> **Status: alpha (v0).** The common path — connect, query, prepared
> statements, transactions, the standard SQL types, result paging, generated
> keys (`LastInsertId`), and reading/writing CLOB/BLOB values — is implemented
> and tested end-to-end against HSQLDB 2.7.4. APIs and behavior may change. Try
> it, report issues; don't bet production on it yet.

```go
import (
	"database/sql"
	_ "github.com/rennerdo30/go-hsql"
)

db, err := sql.Open("hsql", "hsql://SA@localhost:9001/mydb")
```

## Connection string

```
hsql://[user[:password]@]host[:port]/dbAlias[?param=value&...]
hsqls://...   # same, over TLS
```

- `host` defaults to `localhost`, `port` to `9001`.
- `user` defaults to `SA` (HSQLDB's default administrative user).
- `dbAlias` is the **server-side database alias** (the `--dbname.N` the server
  was started with), not a file path.

Query parameters:

| Param       | Meaning                                        | Default |
|-------------|------------------------------------------------|---------|
| `zone`      | session time-zone id sent at connect           | `UTC`   |
| `tzoffset`  | session time-zone offset from UTC, in seconds  | `0`     |
| `fetchsize` | rows per result-set block (0 = all at once)    | `0`     |
| `tlsconfig` | registered TLS config name for `hsqls://`      | unset   |

## Supported features

- Connect / authenticate / ping over the native protocol (compat version 2.x).
- Direct statements (`db.Exec`, `db.Query`) and prepared statements with `?`
  parameters (`db.Prepare`, parameterized `Query`/`Exec`).
- Transactions: `db.BeginTx` with commit/rollback (binary `SETSESSIONATTR` /
  `ENDTRAN`, matching the Java client), isolation levels, read-only.
  Driver-specific savepoint helpers are available through `db.Conn(ctx).Raw`.
- Two-phase commit: `PrepareCommit(ctx)` via `db.Conn(ctx).Raw`.
- Result-set block paging via `REQUESTDATA` for large results.
- Type mapping: INTEGER, BIGINT, SMALLINT, TINYINT, REAL/FLOAT/DOUBLE, BOOLEAN,
  CHAR/VARCHAR (Java modified-UTF-8, full Unicode), DECIMAL/NUMERIC (as string,
  arbitrary precision), DATE/TIME/TIMESTAMP (± time zone), BINARY/VARBINARY,
  BIT, UUID, INTERVAL, and ARRAY result values. NULLs via `sql.Null*`.
- Reading **CLOB/BLOB** values (resolved via the `LARGE_OBJECT_OP` sub-protocol,
  fetched in chunks).
- Writing **CLOB/BLOB** values via prepared-statement parameters.
  Use `hsql.NewBlob(reader, length)` / `hsql.NewClob(reader, length)` for
  streaming binds; pass a negative length when the stream length is unknown.
- Structured ARRAY parameters via `hsql.NewArray(...)`; lossless typed ARRAY
  result scanning via `hsql.ScanArray(&slice)` (string elements are quoted and
  escaped in the text form, so commas, quotes and the literal NULL round-trip).
- Stored-procedure **OUT / INOUT parameters** via the standard `sql.Out`
  wrapper on `CALL` statements; procedures returning result sets deliver them
  through `Query` (with `NextResultSet` for multiple).
- **Updatable result sets** (`UPDATE_RESULT` sub-protocol) via
  `db.Conn(ctx).Raw`: `QueryUpdatable` opens an updatable cursor whose rows can
  be updated, deleted and inserted in place (JDBC `CONCUR_UPDATABLE` parity).
- Session introspection via `db.Conn(ctx).Raw`: `SessionInfo` reports the
  server-side isolation level, autocommit, read-only state, catalog and time
  zone over the native `GETSESSIONATTR` protocol.
- Native batch execution via `db.Conn(ctx).Raw`: direct-SQL (`ExecBatch`) and
  prepared-statement (`ExecPreparedBatch`) using `BATCHEXECDIRECT`/`BATCHEXECUTE`.
- Statement cancellation: context cancel sends a protocol `SQLCANCEL` on a side
  connection (like the Java client) so the server aborts the running statement.
- **`LastInsertId`** via generated keys (works for `IDENTITY` columns on both
  direct and prepared inserts).
- Column introspection via `sql.Rows.ColumnTypes()` (type name, scan type,
  nullability, length, decimal precision/scale).
- Context cancellation / deadlines, and `ErrBadConn` handling for pool health.
- Errors surface as `*hsql.Error` carrying `Message`, `SQLState`, `ErrorCode`.

## Compatibility with the HSQLDB Java client

Interoperability with a real HSQLDB Java server is proven: the entire test suite
runs against the actual `org.hsqldb` server. The wire protocol, transaction
control, LOBs, batches, generated keys, cancellation, and 2PC use the same
messages the reference Java client sends.

Feature parity notes:

- **Updatable result sets** (`UPDATE_RESULT`) are supported through the
  driver-specific `QueryUpdatable` API (via `Conn.Raw`), since `database/sql`
  itself is forward-only/read-only. As in JDBC, the query must be an updatable
  single-table SELECT (no ORDER BY, DISTINCT, GROUP BY, aggregates or joins) or
  the server downgrades the cursor and `QueryUpdatable` returns an error.
- **Stored-procedure OUT / INOUT parameters** are supported with `sql.Out`.
- **ARRAY results** are delivered in a lossless text form: numeric, boolean and
  NULL elements are bare, strings are quoted/escaped, binary is hex, temporal
  values are quoted RFC 3339. `hsql.ScanArray` parses it into typed slices
  (`[]int64`, `[]string`, `[]float64`, `[]bool`, `[]time.Time`, `[][]byte`,
  `[]any`, ...).
- **XA / distributed transactions** — single-connection 2PC (`PrepareCommit`) is
  supported; the `javax.transaction` XA resource model has no Go equivalent, so
  the XA wrapper objects of the Java client are out of scope.

## Development

The protocol was reverse-engineered from the HSQLDB 2.7.4 Java source. The test
suite runs against a real server: the source is cloned and compiled to a jar
under `.hsqldb/` (git-ignored), and integration tests boot it automatically
(skipping if Java or the jar are absent).

```
go test ./...              # unit + integration (needs Java + .hsqldb/hsqldb.jar)
go test ./internal/...     # protocol codec unit tests only (no server)
```

## License

The driver code is original. `.hsqldb/` contains a local copy of the HSQLDB
project (BSD-style HyperSQL license) used only as reference and as a test
server; it is git-ignored and not distributed with this driver.
