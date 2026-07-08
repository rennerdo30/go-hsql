package hsql

import (
	"database/sql/driver"
	"reflect"
	"time"

	"github.com/rennerdo30/go-hsql/internal/proto"
)

// These optional interfaces let database/sql expose column type information via
// sql.Rows.ColumnTypes().
var (
	_ driver.RowsColumnTypeDatabaseTypeName = (*rows)(nil)
	_ driver.RowsColumnTypeLength           = (*rows)(nil)
	_ driver.RowsColumnTypePrecisionScale   = (*rows)(nil)
	_ driver.RowsColumnTypeScanType         = (*rows)(nil)
	_ driver.RowsColumnTypeNullable         = (*rows)(nil)
)

// ColumnTypeDatabaseTypeName returns the HSQLDB type name for a column.
func (r *rows) ColumnTypeDatabaseTypeName(index int) string {
	if r.meta == nil || index >= len(r.meta.Columns) {
		return ""
	}
	return typeName(r.meta.Columns[index].Type.Code)
}

// ColumnTypeScanType returns the Go type produced when scanning a column.
func (r *rows) ColumnTypeScanType(index int) reflect.Type {
	if r.meta == nil || index >= len(r.meta.Columns) {
		return reflect.TypeOf(new(any)).Elem()
	}
	return scanType(r.meta.Columns[index].Type.Code)
}

// ColumnTypeNullable reports whether a column may be NULL. HSQLDB encodes
// nullability in the low bits of the per-column attribute byte.
func (r *rows) ColumnTypeNullable(index int) (nullable, ok bool) {
	if r.meta == nil || index >= len(r.meta.Columns) {
		return false, false
	}
	// Nullability is bits 0-1 of the attribute byte, using java.sql
	// ResultSetMetaData constants: 0 = columnNoNulls, 1 = columnNullable,
	// 2 = columnNullableUnknown.
	const (
		columnNoNulls  = 0
		columnNullable = 1
	)
	switch r.meta.Columns[index].Attrs & 0x03 {
	case columnNoNulls:
		return false, true
	case columnNullable:
		return true, true
	default:
		return false, false // unknown
	}
}

// ColumnTypeLength returns the declared maximum length for variable-length
// character, binary, bit, and LOB columns.
func (r *rows) ColumnTypeLength(index int) (length int64, ok bool) {
	if r.meta == nil || index >= len(r.meta.Columns) {
		return 0, false
	}
	col := r.meta.Columns[index].Type
	switch col.Code {
	case proto.SQLChar, proto.SQLVarchar, proto.SQLBinary, proto.SQLVarbinary,
		proto.SQLBit, proto.SQLBitVarying, proto.SQLBlob, proto.SQLClob:
		if col.Precision < 0 {
			return 0, false
		}
		return col.Precision, true
	default:
		return 0, false
	}
}

// ColumnTypePrecisionScale returns DECIMAL/NUMERIC precision and scale.
func (r *rows) ColumnTypePrecisionScale(index int) (precision, scale int64, ok bool) {
	if r.meta == nil || index >= len(r.meta.Columns) {
		return 0, 0, false
	}
	col := r.meta.Columns[index].Type
	switch col.Code {
	case proto.SQLDecimal, proto.SQLNumeric:
		if col.Precision < 0 {
			return 0, 0, false
		}
		return col.Precision, int64(col.Scale), true
	default:
		return 0, 0, false
	}
}

func typeName(code proto.TypeCode) string {
	switch code {
	case proto.SQLChar:
		return "CHAR"
	case proto.SQLVarchar:
		return "VARCHAR"
	case proto.SQLNumeric:
		return "NUMERIC"
	case proto.SQLDecimal:
		return "DECIMAL"
	case proto.SQLInteger:
		return "INTEGER"
	case proto.SQLSmallint:
		return "SMALLINT"
	case proto.SQLTinyint:
		return "TINYINT"
	case proto.SQLBigint:
		return "BIGINT"
	case proto.SQLReal:
		return "REAL"
	case proto.SQLFloat:
		return "FLOAT"
	case proto.SQLDouble:
		return "DOUBLE"
	case proto.SQLBoolean:
		return "BOOLEAN"
	case proto.SQLDate:
		return "DATE"
	case proto.SQLTime:
		return "TIME"
	case proto.SQLTimeTZ:
		return "TIME WITH TIME ZONE"
	case proto.SQLTimestamp:
		return "TIMESTAMP"
	case proto.SQLTimestampTZ:
		return "TIMESTAMP WITH TIME ZONE"
	case proto.SQLIntervalYear:
		return "INTERVAL YEAR"
	case proto.SQLIntervalMonth:
		return "INTERVAL MONTH"
	case proto.SQLIntervalDay:
		return "INTERVAL DAY"
	case proto.SQLIntervalHour:
		return "INTERVAL HOUR"
	case proto.SQLIntervalMinute:
		return "INTERVAL MINUTE"
	case proto.SQLIntervalSecond:
		return "INTERVAL SECOND"
	case proto.SQLIntervalYearToMonth:
		return "INTERVAL YEAR TO MONTH"
	case proto.SQLIntervalDayToHour:
		return "INTERVAL DAY TO HOUR"
	case proto.SQLIntervalDayToMinute:
		return "INTERVAL DAY TO MINUTE"
	case proto.SQLIntervalDayToSecond:
		return "INTERVAL DAY TO SECOND"
	case proto.SQLIntervalHourToMinute:
		return "INTERVAL HOUR TO MINUTE"
	case proto.SQLIntervalHourToSecond:
		return "INTERVAL HOUR TO SECOND"
	case proto.SQLIntervalMinuteToSecond:
		return "INTERVAL MINUTE TO SECOND"
	case proto.SQLBinary:
		return "BINARY"
	case proto.SQLVarbinary:
		return "VARBINARY"
	case proto.SQLBit, proto.SQLBitVarying:
		return "BIT"
	case proto.SQLBlob:
		return "BLOB"
	case proto.SQLClob:
		return "CLOB"
	case proto.SQLGuid:
		return "UUID"
	case proto.SQLArray:
		return "ARRAY"
	case proto.SQLOther:
		return "OTHER"
	default:
		return "OTHER"
	}
}

func scanType(code proto.TypeCode) reflect.Type {
	switch code {
	case proto.SQLTinyint, proto.SQLSmallint, proto.SQLInteger, proto.SQLBigint:
		return reflect.TypeOf(int64(0))
	case proto.SQLReal, proto.SQLFloat, proto.SQLDouble:
		return reflect.TypeOf(float64(0))
	case proto.SQLBoolean:
		return reflect.TypeOf(false)
	case proto.SQLChar, proto.SQLVarchar, proto.SQLDecimal, proto.SQLNumeric,
		proto.SQLIntervalYear, proto.SQLIntervalMonth, proto.SQLIntervalDay,
		proto.SQLIntervalHour, proto.SQLIntervalMinute, proto.SQLIntervalSecond,
		proto.SQLIntervalYearToMonth, proto.SQLIntervalDayToHour,
		proto.SQLIntervalDayToMinute, proto.SQLIntervalDayToSecond,
		proto.SQLIntervalHourToMinute, proto.SQLIntervalHourToSecond,
		proto.SQLIntervalMinuteToSecond, proto.SQLArray:
		return reflect.TypeOf("")
	case proto.SQLBinary, proto.SQLVarbinary, proto.SQLGuid, proto.SQLOther:
		return reflect.TypeOf([]byte(nil))
	case proto.SQLDate, proto.SQLTime, proto.SQLTimeTZ, proto.SQLTimestamp, proto.SQLTimestampTZ:
		return reflect.TypeOf(time.Time{})
	default:
		return reflect.TypeOf(new(any)).Elem()
	}
}
