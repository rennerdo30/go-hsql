package proto

import (
	"bufio"
	"bytes"
	"math/big"
	"testing"
)

func TestPrimitiveRoundTrip(t *testing.T) {
	w := NewRowOutput()
	w.WriteU8(0xAB)
	w.WriteBool(true)
	w.WriteShort(-12345)
	w.WriteInt(-2030400)
	w.WriteLong(9223372036854775807)
	w.WriteDouble(3.14159265358979)
	w.WriteString("hello, 世界\x00tail")
	w.WriteBytes([]byte{1, 2, 3, 0xff})

	r := NewRowInput(w.Bytes())
	if got := r.ReadU8(); got != 0xAB {
		t.Errorf("u8 = %#x", got)
	}
	if got := r.ReadBool(); got != true {
		t.Errorf("bool = %v", got)
	}
	if got := r.ReadShort(); got != -12345 {
		t.Errorf("short = %d", got)
	}
	if got := r.ReadInt(); got != -2030400 {
		t.Errorf("int = %d", got)
	}
	if got := r.ReadLong(); got != 9223372036854775807 {
		t.Errorf("long = %d", got)
	}
	if got := r.ReadDouble(); got != 3.14159265358979 {
		t.Errorf("double = %v", got)
	}
	if got := r.ReadString(); got != "hello, 世界\x00tail" {
		t.Errorf("string = %q", got)
	}
	if got := r.ReadBytes(); !bytes.Equal(got, []byte{1, 2, 3, 0xff}) {
		t.Errorf("bytes = %v", got)
	}
	if err := r.Err(); err != nil {
		t.Fatalf("err = %v", err)
	}
	if r.Remaining() != 0 {
		t.Errorf("remaining = %d, want 0", r.Remaining())
	}
}

func TestModifiedUTF8NullByte(t *testing.T) {
	// U+0000 must encode to the two-byte sequence 0xC0 0x80 (Java modified UTF-8).
	enc := encodeModifiedUTF8("\x00")
	if !bytes.Equal(enc, []byte{0xC0, 0x80}) {
		t.Fatalf("NUL encoded as %v, want [C0 80]", enc)
	}
	if got := decodeModifiedUTF8(enc); got != "\x00" {
		t.Fatalf("decode = %q", got)
	}
}

func TestDecimalRoundTrip(t *testing.T) {
	cases := []struct {
		unscaled string
		scale    int32
	}{
		{"12345678", 3},
		{"-98765", 2},
		{"0", 0},
		{"170141183460469231731687303715884105727", 5}, // > 64-bit
		{"-170141183460469231731687303715884105728", 0},
	}
	for _, tc := range cases {
		u, _ := new(big.Int).SetString(tc.unscaled, 10)
		w := NewRowOutput()
		w.WriteDecimal(u, tc.scale)
		r := NewRowInput(w.Bytes())
		gotU, gotS := r.ReadDecimal()
		if gotU.Cmp(u) != 0 || gotS != tc.scale {
			t.Errorf("decimal %s/%d round-tripped to %s/%d", tc.unscaled, tc.scale, gotU, gotS)
		}
	}
}

func TestFrameRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	bw := bufio.NewWriter(&buf)
	payload := []byte("payload-bytes")
	if err := WriteFrame(bw, ModeExecDirect, payload); err != nil {
		t.Fatal(err)
	}
	if err := WriteTerminator(bw); err != nil {
		t.Fatal(err)
	}
	bw.Flush()

	br := bufio.NewReader(&buf)
	mode, got, err := ReadFrame(br)
	if err != nil {
		t.Fatal(err)
	}
	if mode != ModeExecDirect {
		t.Errorf("mode = %d", mode)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("payload = %q", got)
	}
	// Next frame is the NONE terminator.
	m2, _, err := ReadFrame(br)
	if err != nil {
		t.Fatal(err)
	}
	if m2 != ModeNone {
		t.Errorf("terminator mode = %d, want 0", m2)
	}
}

func TestValueRoundTrip(t *testing.T) {
	cases := []struct {
		code ColumnType
		val  any
	}{
		{ColumnType{Code: SQLInteger}, int64(42)},
		{ColumnType{Code: SQLBigint}, int64(-9000000000)},
		{ColumnType{Code: SQLDouble}, 2.5},
		{ColumnType{Code: SQLBoolean}, true},
		{ColumnType{Code: SQLVarchar}, "text"},
		{ColumnType{Code: SQLVarbinary}, []byte{9, 8, 7}},
	}
	for _, tc := range cases {
		w := NewRowOutput()
		if err := w.WriteValue(tc.code, tc.val); err != nil {
			t.Fatalf("write %v: %v", tc.val, err)
		}
		r := NewRowInput(w.Bytes())
		got := r.ReadValue(tc.code)
		if err := r.Err(); err != nil {
			t.Fatalf("read %v: %v", tc.val, err)
		}
		switch want := tc.val.(type) {
		case []byte:
			if g, ok := got.([]byte); !ok || !bytes.Equal(g, want) {
				t.Errorf("value %v round-tripped to %v", tc.val, got)
			}
		default:
			if got != tc.val {
				t.Errorf("value %v round-tripped to %v", tc.val, got)
			}
		}
	}

	// NULL round-trip.
	w := NewRowOutput()
	if err := w.WriteValue(ColumnType{Code: SQLInteger}, nil); err != nil {
		t.Fatal(err)
	}
	r := NewRowInput(w.Bytes())
	if got := r.ReadValue(ColumnType{Code: SQLInteger}); got != nil {
		t.Errorf("null round-tripped to %v", got)
	}
}
