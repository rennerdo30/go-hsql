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
- Structured ARRAY parameters via `hsql.NewArray(...)`; typed ARRAY result
  scanning via `hsql.ScanArray(&slice)`.
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

Remaining differences (all either niche or with no `database/sql` equivalent):

- **Scrollable / updatable result sets** (`UPDATE_RESULT`) — `database/sql` is
  forward-only, so these have no equivalent and are not implemented.
- **Stored-procedure OUT parameters** — `CALL` statements that return result
  sets or update counts work via the normal paths; bound OUT parameters are not
  exposed (also a `database/sql` limitation).
- **ARRAY results** are delivered as text (`[a,b,c]`); use `hsql.ScanArray` for
  typed slices. The text form is ambiguous for string elements containing
  commas — use typed numeric/boolean arrays for lossless results.
- **XA / distributed transactions** — single-connection 2PC (`PrepareCommit`) is
  supported; the `javax.transaction` XA resource model has no Go equivalent.

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
