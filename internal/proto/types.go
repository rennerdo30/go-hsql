package proto

import (
	"fmt"
	"math/big"
	"strings"
	"time"
)

// ColumnType describes one column or parameter as carried in ResultMetaData.
type ColumnType struct {
	Code      TypeCode
	BaseCode  TypeCode // element type for array types; 0 otherwise
	Precision int64
	Scale     int32
}

// IsArray reports whether the type is a SQL array type. HSQLDB encodes an extra
// base-type code for array columns.
func (c ColumnType) IsArray() bool { return c.Code == SQLArray }

// WriteDataType writes a per-column type descriptor:
//
//	short typeCode [short baseTypeCode if array] long precision int scale
func (w *RowOutput) WriteDataType(c ColumnType) {
	w.WriteType(c.Code)
	if c.IsArray() {
		w.WriteType(c.BaseCode)
	}
	w.WriteLong(c.Precision)
	w.WriteInt(c.Scale)
}

// WriteDataTypeSimple writes a code-only type descriptor, matching
// ResultMetaData.writeDataTypeCodes.
func (w *RowOutput) WriteDataTypeSimple(c ColumnType) {
	w.WriteType(c.Code)
	if c.IsArray() {
		w.WriteType(c.BaseCode)
	}
}

// ReadDataType reads a full per-column type descriptor (code [+ array base]
// precision scale), matching ResultMetaData.readDataType.
func (r *RowInput) ReadDataType() ColumnType {
	c := ColumnType{Code: r.ReadType()}
	if c.IsArray() {
		c.BaseCode = r.ReadType()
	}
	c.Precision = r.ReadLong()
	c.Scale = r.ReadInt()
	return c
}

// ReadDataTypeSimple reads a code-only type descriptor (code [+ array base], no
// precision/scale), matching ResultMetaData.readDataTypeSimple used by the
// SIMPLE_RESULT_METADATA and UPDATE_RESULT_METADATA branches.
func (r *RowInput) ReadDataTypeSimple() ColumnType {
	c := ColumnType{Code: r.ReadType()}
	if c.IsArray() {
		c.BaseCode = r.ReadType()
	}
	return c
}

// The epoch-seconds temporal encodings all reference UTC.
const secondsPerDay = 86400

// WriteValue writes a single column/parameter value for type t. It first writes
// the 1-byte null flag (0 = null, nothing follows; 1 = present), then the
// type-specific bytes. v must be nil or one of the driver value types
// (int64, float64, bool, []byte, string, time.Time) or a Decimal.
func (w *RowOutput) WriteValue(c ColumnType, v any) error {
	if v == nil {
		w.WriteU8(0)
		return nil
	}
	w.WriteU8(1)
	switch c.Code {
	case SQLChar, SQLVarchar:
		w.WriteString(asString(v))
	case SQLTinyint, SQLSmallint:
		w.WriteShort(int16(asInt64(v)))
	case SQLInteger:
		w.WriteInt(int32(asInt64(v)))
	case SQLBigint:
		w.WriteLong(asInt64(v))
	case SQLReal, SQLFloat, SQLDouble:
		w.WriteDouble(asFloat64(v))
	case SQLBoolean:
		w.WriteBool(asBool(v))
	case SQLDecimal, SQLNumeric:
		d, err := asDecimal(v, c.Scale)
		if err != nil {
			return err
		}
		w.WriteDecimal(d.Unscaled, d.Scale)
	case SQLDate:
		t := asTime(v)
		w.WriteLong(dateSeconds(t))
	case SQLTime, SQLTimeTZ:
		t := asTime(v)
		w.WriteInt(int32(timeOfDaySeconds(t)))
		w.WriteInt(int32(t.Nanosecond()))
		if c.Code == SQLTimeTZ {
			w.WriteInt(int32(zoneSeconds(t)))
		}
	case SQLTimestamp, SQLTimestampTZ:
		t := asTime(v)
		w.WriteLong(t.Unix())
		w.WriteInt(int32(t.Nanosecond()))
		if c.Code == SQLTimestampTZ {
			w.WriteInt(int32(zoneSeconds(t)))
		}
	case SQLBinary, SQLVarbinary:
		w.WriteBytes(asBytes(v))
	case SQLGuid, SQLOther:
		w.WriteBytes(asBytes(v))
	case SQLBit, SQLBitVarying:
		b := asBytes(v)
		w.WriteInt(int32(len(b) * 8)) // bit length prefix, then raw bytes (no byte-len)
		w.WriteRaw(b)
	case SQLArray:
		arr, ok := v.(ArrayValue)
		if !ok {
			return fmt.Errorf("hsql/proto: ARRAY parameter requires ArrayValue, got %T", v)
		}
		elemType := ColumnType{Code: c.BaseCode}
		w.WriteInt(int32(len(arr.Values)))
		for _, elem := range arr.Values {
			if err := w.WriteValue(elemType, elem); err != nil {
				return err
			}
		}
	case SQLClob, SQLBlob:
		ref, ok := v.(LobRef)
		if !ok {
			return fmt.Errorf("hsql/proto: LOB parameter type %d requires LobRef, got %T", c.Code, v)
		}
		w.WriteLong(ref.ID)
	default:
		return fmt.Errorf("hsql/proto: unsupported write type code %d", c.Code)
	}
	return nil
}

// ReadValue reads a single column value for type t, returning a driver value
// (int64, float64, bool, string, []byte, time.Time) or nil.
func (r *RowInput) ReadValue(c ColumnType) any {
	if r.ReadU8() == 0 {
		return nil
	}
	switch c.Code {
	case SQLChar, SQLVarchar:
		return r.ReadString()
	case SQLTinyint, SQLSmallint:
		return int64(r.ReadShort())
	case SQLInteger:
		return int64(r.ReadInt())
	case SQLBigint:
		return r.ReadLong()
	case SQLReal, SQLFloat, SQLDouble:
		return r.ReadDouble()
	case SQLBoolean:
		return r.ReadBool()
	case SQLDecimal, SQLNumeric:
		unscaled, scale := r.ReadDecimal()
		return formatDecimal(unscaled, scale)
	case SQLDate:
		sec := r.ReadLong()
		return time.Unix(sec, 0).UTC()
	case SQLTime, SQLTimeTZ:
		sec := r.ReadInt()
		nanos := r.ReadInt()
		loc := time.UTC
		if c.Code == SQLTimeTZ {
			zone := r.ReadInt()
			loc = zoneLocation(int(zone))
		}
		return time.Date(1970, 1, 1, 0, 0, int(sec), int(nanos), loc)
	case SQLTimestamp, SQLTimestampTZ:
		sec := r.ReadLong()
		nanos := r.ReadInt()
		loc := time.UTC
		if c.Code == SQLTimestampTZ {
			zone := r.ReadInt()
			loc = zoneLocation(int(zone))
		}
		return time.Unix(sec, int64(nanos)).In(loc)
	case SQLBinary, SQLVarbinary:
		return r.ReadBytes()
	case SQLGuid, SQLOther:
		return r.ReadBytes()
	case SQLBit, SQLBitVarying:
		bits := int(r.ReadInt())
		n := (bits + 7) / 8
		if r.err != nil || n < 0 || !r.need(n) {
			return nil
		}
		out := make([]byte, n)
		copy(out, r.buf[r.pos:r.pos+n])
		r.pos += n
		return out
	case SQLIntervalYear, SQLIntervalMonth, SQLIntervalYearToMonth:
		return formatYearMonthInterval(c.Code, r.ReadLong())
	case SQLIntervalDay, SQLIntervalHour, SQLIntervalMinute, SQLIntervalSecond,
		SQLIntervalDayToHour, SQLIntervalDayToMinute, SQLIntervalDayToSecond,
		SQLIntervalHourToMinute, SQLIntervalHourToSecond, SQLIntervalMinuteToSecond:
		return formatDaySecondInterval(c.Code, r.ReadLong(), r.ReadInt())
	case SQLArray:
		return r.readArray(c)
	case SQLClob, SQLBlob:
		// LOBs are delivered as an int64 id; the driver resolves the payload via
		// the LOB sub-protocol. Return a LobRef carrying the id.
		return LobRef{ID: r.ReadLong(), IsClob: c.Code == SQLClob}
	default:
		// Unknown type: we cannot know its length, so flag the stream as
		// broken rather than silently desyncing.
		r.err = fmt.Errorf("hsql/proto: unsupported read type code %d", c.Code)
		return nil
	}
}

// Decimal is an arbitrary-precision decimal as unscaled integer plus scale.
type Decimal struct {
	Unscaled *big.Int
	Scale    int32
}

// ArrayValue is the internal representation used to encode a SQL ARRAY
// parameter. The element type comes from ColumnType.BaseCode.
type ArrayValue struct {
	Values []any
}

func dateSeconds(t time.Time) int64 {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC).Unix()
}

func timeOfDaySeconds(t time.Time) int {
	return t.Hour()*3600 + t.Minute()*60 + t.Second()
}

func zoneSeconds(t time.Time) int {
	_, off := t.Zone()
	return off
}

func zoneLocation(sec int) *time.Location {
	if sec == 0 {
		return time.UTC
	}
	return time.FixedZone("", sec)
}

func formatDecimal(unscaled *big.Int, scale int32) string {
	if scale == 0 {
		return unscaled.String()
	}
	neg := unscaled.Sign() < 0
	digits := new(big.Int).Abs(unscaled).String()
	s := int(scale)
	if s < 0 {
		// Negative scale multiplies by 10^-scale.
		digits += strings.Repeat("0", -s)
		if neg {
			return "-" + digits
		}
		return digits
	}
	for len(digits) <= s {
		digits = "0" + digits
	}
	intPart := digits[:len(digits)-s]
	fracPart := digits[len(digits)-s:]
	out := intPart + "." + fracPart
	if neg {
		out = "-" + out
	}
	return out
}

func formatYearMonthInterval(code TypeCode, months int64) string {
	neg := months < 0
	if neg {
		months = -months
	}
	var out string
	switch code {
	case SQLIntervalYear:
		out = fmt.Sprintf("%d", months/12)
	case SQLIntervalMonth:
		out = fmt.Sprintf("%d", months)
	default:
		out = fmt.Sprintf("%d-%02d", months/12, months%12)
	}
	if neg {
		return "-" + out
	}
	return out
}

func formatDaySecondInterval(code TypeCode, seconds int64, nanos int32) string {
	neg := seconds < 0 || nanos < 0
	if neg {
		seconds = -seconds
		nanos = -nanos
	}
	days := seconds / secondsPerDay
	rem := seconds % secondsPerDay
	hours := rem / 3600
	rem %= 3600
	minutes := rem / 60
	secs := rem % 60

	secPart := fmt.Sprintf("%02d", secs)
	if nanos != 0 {
		frac := fmt.Sprintf("%09d", nanos)
		frac = strings.TrimRight(frac, "0")
		secPart += "." + frac
	}

	var out string
	switch code {
	case SQLIntervalDay:
		out = fmt.Sprintf("%d", days)
	case SQLIntervalHour:
		out = fmt.Sprintf("%d", seconds/3600)
	case SQLIntervalMinute:
		out = fmt.Sprintf("%d", seconds/60)
	case SQLIntervalSecond:
		out = strings.TrimLeft(secPart, "0")
		if strings.HasPrefix(out, ".") {
			out = "0" + out
		}
		if out == "" {
			out = "0"
		}
	case SQLIntervalDayToHour:
		out = fmt.Sprintf("%d %02d", days, hours)
	case SQLIntervalDayToMinute:
		out = fmt.Sprintf("%d %02d:%02d", days, hours, minutes)
	case SQLIntervalDayToSecond:
		out = fmt.Sprintf("%d %02d:%02d:%s", days, hours, minutes, secPart)
	case SQLIntervalHourToMinute:
		out = fmt.Sprintf("%d:%02d", seconds/3600, minutes)
	case SQLIntervalHourToSecond:
		out = fmt.Sprintf("%d:%02d:%s", seconds/3600, minutes, secPart)
	default:
		out = fmt.Sprintf("%d:%s", seconds/60, secPart)
	}
	if neg {
		return "-" + out
	}
	return out
}

func (r *RowInput) readArray(c ColumnType) string {
	n := int(r.ReadInt())
	if r.err != nil || n < 0 {
		return ""
	}
	elemType := ColumnType{Code: c.BaseCode}
	parts := make([]string, n)
	for i := 0; i < n; i++ {
		v := r.ReadValue(elemType)
		if v == nil {
			parts[i] = "NULL"
			continue
		}
		switch x := v.(type) {
		case []byte:
			parts[i] = fmt.Sprintf("%x", x)
		case string:
			parts[i] = x
		default:
			parts[i] = fmt.Sprint(x)
		}
	}
	return "[" + strings.Join(parts, ",") + "]"
}

// --- value coercion helpers (inputs come from database/sql driver values) ---

func asString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case []byte:
		return string(x)
	default:
		return fmt.Sprint(x)
	}
}

func asInt64(v any) int64 {
	switch x := v.(type) {
	case int64:
		return x
	case int:
		return int64(x)
	case int32:
		return int64(x)
	case bool:
		if x {
			return 1
		}
		return 0
	case float64:
		return int64(x)
	default:
		return 0
	}
}

func asFloat64(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case float32:
		return float64(x)
	case int64:
		return float64(x)
	default:
		return 0
	}
}

func asBool(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case int64:
		return x != 0
	default:
		return false
	}
}

func asBytes(v any) []byte {
	switch x := v.(type) {
	case []byte:
		return x
	case string:
		return []byte(x)
	default:
		return nil
	}
}

func asTime(v any) time.Time {
	if t, ok := v.(time.Time); ok {
		return t
	}
	return time.Time{}
}

func asDecimal(v any, scale int32) (Decimal, error) {
	switch x := v.(type) {
	case Decimal:
		return x, nil
	case string:
		return parseDecimal(x, scale)
	case []byte:
		return parseDecimal(string(x), scale)
	case int64:
		return Decimal{Unscaled: scaleUnscaled(big.NewInt(x), scale), Scale: scale}, nil
	case float64:
		return parseDecimal(fmt.Sprintf("%v", x), scale)
	default:
		return Decimal{}, fmt.Errorf("hsql/proto: cannot convert %T to decimal", v)
	}
}

func scaleUnscaled(v *big.Int, scale int32) *big.Int {
	if scale <= 0 {
		return v
	}
	mult := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(scale)), nil)
	return new(big.Int).Mul(v, mult)
}

func parseDecimal(s string, scale int32) (Decimal, error) {
	s = strings.TrimSpace(s)
	neg := false
	if strings.HasPrefix(s, "-") {
		neg = true
		s = s[1:]
	} else if strings.HasPrefix(s, "+") {
		s = s[1:]
	}
	intPart, fracPart := s, ""
	if i := strings.IndexByte(s, '.'); i >= 0 {
		intPart, fracPart = s[:i], s[i+1:]
	}
	// Adjust fraction to the requested scale.
	want := int(scale)
	if want < 0 {
		want = 0
	}
	for len(fracPart) < want {
		fracPart += "0"
	}
	if len(fracPart) > want {
		fracPart = fracPart[:want] // truncate excess precision
	}
	digits := intPart + fracPart
	if digits == "" {
		digits = "0"
	}
	unscaled, ok := new(big.Int).SetString(digits, 10)
	if !ok {
		return Decimal{}, fmt.Errorf("hsql/proto: invalid decimal %q", s)
	}
	if neg {
		unscaled.Neg(unscaled)
	}
	return Decimal{Unscaled: unscaled, Scale: scale}, nil
}
