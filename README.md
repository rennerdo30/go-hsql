# go-hsql

A pure-Go [`database/sql`](https://pkg.go.dev/database/sql) driver for
**HSQLDB / HyperSQL**, speaking the native HSQLDB binary network protocol over
TCP (`hsql://` and `hsqls://`). No JDBC, no CGo, no external dependencies — it
talks the wire protocol directly.

> **Status: alpha (v0).** The common path — connect, query, prepared
> statements, transactions, the standard SQL types, and result paging — is
> implemented and tested end-to-end against HSQLDB 2.7.4. LOB streaming, batch
> execution, and `LastInsertId` are not yet supported (see below). APIs and
> behavior may change. Try it, report issues; don't bet production on it yet.

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

## Supported features

- Connect / authenticate / ping over the native protocol (compat version 2.x).
- Direct statements (`db.Exec`, `db.Query`) and prepared statements with `?`
  parameters (`db.Prepare`, parameterized `Query`/`Exec`).
- Transactions: `db.BeginTx` with commit/rollback, isolation levels, read-only.
- Result-set block paging via `REQUESTDATA` for large results.
- Type mapping: INTEGER, BIGINT, SMALLINT, TINYINT, REAL/FLOAT/DOUBLE, BOOLEAN,
  CHAR/VARCHAR (Java modified-UTF-8, full Unicode), DECIMAL/NUMERIC (as string,
  arbitrary precision), DATE/TIME/TIMESTAMP (± time zone), BINARY/VARBINARY,
  BIT. NULLs via `sql.Null*`.
- Column introspection via `sql.Rows.ColumnTypes()`.
- Errors surface as `*hsql.Error` carrying `Message`, `SQLState`, `ErrorCode`.

## Not yet implemented

- LOBs (CLOB/BLOB) are read as their server-side id and surfaced as `NULL`;
  streaming the payload (the `LARGE_OBJECT_OP` sub-protocol) is future work.
- Batch execution (`BATCHEXECUTE`) — `database/sql` has no batch API; the wire
  support is decoded but not exposed.
- `LastInsertId` requires an explicit generated-keys request; use `IDENTITY()`
  or a `RETURNING` clause instead.

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
