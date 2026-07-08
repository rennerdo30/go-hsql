package proto

import (
	"math/big"
	"testing"
)

func TestCoercionHelpers(t *testing.T) {
	if asInt64(int(7)) != 7 || asInt64(int32(8)) != 8 || asInt64(int64(9)) != 9 {
		t.Error("asInt64 integer forms")
	}
	if asInt64(true) != 1 || asInt64(false) != 0 {
		t.Error("asInt64 bool")
	}
	if asInt64(3.9) != 3 {
		t.Error("asInt64 float truncation")
	}
	if asFloat64(float32(1.5)) != 1.5 || asFloat64(int64(2)) != 2 || asFloat64(2.5) != 2.5 {
		t.Error("asFloat64")
	}
	if asBool(int64(1)) != true || asBool(int64(0)) != false {
		t.Error("asBool int")
	}
	if asString([]byte("x")) != "x" || asString(42) != "42" {
		t.Error("asString")
	}
	if string(asBytes("s")) != "s" {
		t.Error("asBytes string")
	}
	if asTime("not a time").IsZero() != true {
		t.Error("asTime non-time -> zero")
	}
}

func TestAsDecimalForms(t *testing.T) {
	d, err := asDecimal(int64(42), 2) // 42 with scale 2 -> unscaled 4200
	if err != nil || d.Unscaled.Int64() != 4200 || d.Scale != 2 {
		t.Fatalf("int64 decimal: %+v %v", d, err)
	}
	d, err = asDecimal("3.5", 2) // -> unscaled 350
	if err != nil || d.Unscaled.Int64() != 350 {
		t.Fatalf("string decimal: %+v %v", d, err)
	}
	d, err = asDecimal(Decimal{Unscaled: big.NewInt(7), Scale: 0}, 0)
	if err != nil || d.Unscaled.Int64() != 7 {
		t.Fatalf("passthrough decimal: %+v %v", d, err)
	}
	if _, err := asDecimal(struct{}{}, 0); err == nil {
		t.Fatal("expected error for unsupported decimal type")
	}
}

func TestReadDataTypeSimple(t *testing.T) {
	w := NewRowOutput()
	w.WriteType(SQLInteger)
	r := NewRowInput(w.Bytes())
	c := r.ReadDataTypeSimple()
	if c.Code != SQLInteger {
		t.Fatalf("simple type = %d", c.Code)
	}
}

func TestParamMetadataDecode(t *testing.T) {
	// Build a PREPARE_ACK with result metadata + param metadata and decode it.
	w := NewRowOutput()
	w.WriteU8(0)    // statementReturnType
	w.WriteLong(77) // statementID
	w.WriteU8(0)    // rsProperties
	// result metadata (RESULT_METADATA, zero columns)
	w.WriteInt(MetaResult)
	w.WriteInt(0) // columnCount
	w.WriteInt(0) // extendedColumnCount
	// param metadata (PARAM_METADATA, one INTEGER param)
	w.WriteInt(MetaParam)
	w.WriteInt(1) // columnCount
	w.WriteDataType(ColumnType{Code: SQLInteger})
	w.WriteString("arg0")
	w.WriteU8(0) // param attrs

	res, err := DecodeResult(ModePrepareAck, w.Bytes())
	if err != nil {
		t.Fatalf("decode prepare_ack: %v", err)
	}
	if res.StatementID != 77 {
		t.Errorf("statementID = %d", res.StatementID)
	}
	if res.ParamMeta == nil || res.ParamMeta.ColumnCount != 1 {
		t.Fatalf("param meta = %+v", res.ParamMeta)
	}
	if res.ParamMeta.Types[0].Code != SQLInteger {
		t.Errorf("param type = %d", res.ParamMeta.Types[0].Code)
	}
	if res.ParamMeta.Columns[0].Label != "arg0" {
		t.Errorf("param label = %q", res.ParamMeta.Columns[0].Label)
	}
}

func TestScaleUnscaled(t *testing.T) {
	got := scaleUnscaled(big.NewInt(3), 4) // 3 * 10^4
	if got.Int64() != 30000 {
		t.Fatalf("scaleUnscaled = %s", got)
	}
	if scaleUnscaled(big.NewInt(5), 0).Int64() != 5 {
		t.Fatal("scaleUnscaled scale 0")
	}
}

func TestSimpleMetadataDecode(t *testing.T) {
	// SIMPLE_RESULT_METADATA carries type codes only (no precision/scale).
	w := NewRowOutput()
	w.WriteInt(MetaSimpleResult)
	w.WriteInt(2)
	w.WriteType(SQLInteger)
	w.WriteType(SQLVarchar)
	r := NewRowInput(w.Bytes())
	m := readMetadata(r)
	if m.MetaType != MetaSimpleResult || len(m.Types) != 2 || m.Types[1].Code != SQLVarchar {
		t.Fatalf("simple metadata = %+v", m)
	}
}
