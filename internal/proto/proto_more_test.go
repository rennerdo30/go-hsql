package proto

import (
	"bufio"
	"bytes"
	"math/big"
	"testing"
	"time"
)

func TestTemporalValueRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		col  ColumnType
		in   time.Time
		// checkers work on the decoded time
		check func(t *testing.T, got time.Time)
	}{
		{
			name: "date",
			col:  ColumnType{Code: SQLDate},
			in:   time.Date(2023, 11, 15, 9, 30, 0, 0, time.UTC),
			check: func(t *testing.T, got time.Time) {
				if got.Year() != 2023 || got.Month() != 11 || got.Day() != 15 {
					t.Errorf("date = %v", got)
				}
			},
		},
		{
			name: "timestamp",
			col:  ColumnType{Code: SQLTimestamp},
			in:   time.Date(2023, 11, 15, 14, 30, 45, 123456000, time.UTC),
			check: func(t *testing.T, got time.Time) {
				if got.Hour() != 14 || got.Minute() != 30 || got.Second() != 45 || got.Nanosecond() != 123456000 {
					t.Errorf("timestamp = %v (nanos %d)", got, got.Nanosecond())
				}
			},
		},
		{
			name: "time",
			col:  ColumnType{Code: SQLTime},
			in:   time.Date(1970, 1, 1, 13, 5, 7, 0, time.UTC),
			check: func(t *testing.T, got time.Time) {
				if got.Hour() != 13 || got.Minute() != 5 || got.Second() != 7 {
					t.Errorf("time = %v", got)
				}
			},
		},
		{
			name: "timestamp_tz",
			col:  ColumnType{Code: SQLTimestampTZ},
			in:   time.Date(2023, 6, 1, 12, 0, 0, 0, time.FixedZone("", 2*3600)),
			check: func(t *testing.T, got time.Time) {
				_, off := got.Zone()
				if off != 2*3600 {
					t.Errorf("tz offset = %d, want 7200", off)
				}
			},
		},
		{
			name: "time_tz",
			col:  ColumnType{Code: SQLTimeTZ},
			in:   time.Date(1970, 1, 1, 8, 15, 0, 0, time.FixedZone("", -5*3600)),
			check: func(t *testing.T, got time.Time) {
				if got.Hour() != 8 || got.Minute() != 15 {
					t.Errorf("time_tz = %v", got)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := NewRowOutput()
			if err := w.WriteValue(tc.col, tc.in); err != nil {
				t.Fatalf("write: %v", err)
			}
			r := NewRowInput(w.Bytes())
			got := r.ReadValue(tc.col)
			if err := r.Err(); err != nil {
				t.Fatalf("read: %v", err)
			}
			gt, ok := got.(time.Time)
			if !ok {
				t.Fatalf("got %T, want time.Time", got)
			}
			tc.check(t, gt)
		})
	}
}

func TestDecimalValueAndFormat(t *testing.T) {
	col := ColumnType{Code: SQLDecimal, Scale: 3}
	w := NewRowOutput()
	if err := w.WriteValue(col, "-12345.678"); err != nil {
		t.Fatalf("write: %v", err)
	}
	r := NewRowInput(w.Bytes())
	got := r.ReadValue(col)
	if got != "-12345.678" {
		t.Fatalf("decimal value = %v, want -12345.678", got)
	}

	// formatDecimal edge cases.
	if s := formatDecimal(big.NewInt(5), 0); s != "5" {
		t.Errorf("scale 0: %q", s)
	}
	if s := formatDecimal(big.NewInt(5), -2); s != "500" {
		t.Errorf("negative scale: %q", s)
	}
	if s := formatDecimal(big.NewInt(-5), 3); s != "-0.005" {
		t.Errorf("small negative: %q", s)
	}
}

func TestBitValueRoundTrip(t *testing.T) {
	col := ColumnType{Code: SQLBit}
	w := NewRowOutput()
	if err := w.WriteValue(col, []byte{0xAB, 0xCD}); err != nil {
		t.Fatalf("write: %v", err)
	}
	r := NewRowInput(w.Bytes())
	got, ok := r.ReadValue(col).([]byte)
	if !ok || len(got) != 2 || got[0] != 0xAB || got[1] != 0xCD {
		t.Fatalf("bit round-trip = %v", got)
	}
}

func TestAdditionalValueTypes(t *testing.T) {
	guid := []byte{0x01, 0x23, 0x45, 0x67, 0x89, 0xab, 0xcd, 0xef, 0xfe, 0xdc, 0xba, 0x98, 0x76, 0x54, 0x32, 0x10}
	w := NewRowOutput()
	if err := w.WriteValue(ColumnType{Code: SQLGuid}, guid); err != nil {
		t.Fatalf("write guid: %v", err)
	}
	if err := w.WriteValue(ColumnType{Code: SQLOther}, []byte{1, 2, 3}); err != nil {
		t.Fatalf("write other: %v", err)
	}
	w.WriteU8(1)
	w.WriteLong(14) // 1 year, 2 months
	w.WriteU8(1)
	w.WriteLong(3*secondsPerDay + 4*3600 + 5*60 + 6)
	w.WriteInt(789000000)
	w.WriteU8(1)
	w.WriteInt(3)
	for _, v := range []int32{1, 2, 3} {
		w.WriteU8(1)
		w.WriteInt(v)
	}
	if err := w.WriteValue(ColumnType{Code: SQLArray, BaseCode: SQLInteger}, ArrayValue{Values: []any{int64(7), nil, int64(8)}}); err != nil {
		t.Fatalf("write array param: %v", err)
	}

	r := NewRowInput(w.Bytes())
	if got := r.ReadValue(ColumnType{Code: SQLGuid}); !bytes.Equal(got.([]byte), guid) {
		t.Fatalf("guid = %x", got)
	}
	if got := r.ReadValue(ColumnType{Code: SQLOther}); !bytes.Equal(got.([]byte), []byte{1, 2, 3}) {
		t.Fatalf("other = %x", got)
	}
	if got := r.ReadValue(ColumnType{Code: SQLIntervalYearToMonth}); got != "1-02" {
		t.Fatalf("year-month interval = %v", got)
	}
	if got := r.ReadValue(ColumnType{Code: SQLIntervalDayToSecond}); got != "3 04:05:06.789" {
		t.Fatalf("day-second interval = %v", got)
	}
	if got := r.ReadValue(ColumnType{Code: SQLArray, BaseCode: SQLInteger}); got != "[1,2,3]" {
		t.Fatalf("array = %v", got)
	}
	if got := r.ReadValue(ColumnType{Code: SQLArray, BaseCode: SQLInteger}); got != "[7,NULL,8]" {
		t.Fatalf("array param round-trip = %v", got)
	}
	if err := (NewRowOutput()).WriteValue(ColumnType{Code: SQLArray, BaseCode: SQLInteger}, []any{int64(1)}); err == nil {
		t.Fatal("expected error writing raw ARRAY param")
	}
	if err := r.Err(); err != nil {
		t.Fatalf("read additional types: %v", err)
	}
}

func TestLOBValueReturnsRef(t *testing.T) {
	// LOB params are encoded as server-side ids; reading returns a LobRef
	// carrying the id and keeps the stream aligned.
	col := ColumnType{Code: SQLBlob}
	out := NewRowOutput()
	if err := out.WriteValue(col, LobRef{ID: -7}); err != nil {
		t.Fatalf("write BLOB ref: %v", err)
	}
	in := NewRowInput(out.Bytes())
	if in.ReadU8() != 1 || in.ReadLong() != -7 {
		t.Fatal("BLOB ref was not encoded as not-null id")
	}
	if err := (NewRowOutput()).WriteValue(col, []byte("x")); err == nil {
		t.Fatal("expected error writing raw BLOB param")
	}
	w := NewRowOutput()
	w.WriteU8(1)       // not-null flag
	w.WriteLong(999)   // lob id
	w.WriteInt(0x1234) // trailing marker to prove alignment
	r := NewRowInput(w.Bytes())
	ref, ok := r.ReadValue(col).(LobRef)
	if !ok || ref.ID != 999 || ref.IsClob {
		t.Fatalf("BLOB read = %v, want LobRef{999,false}", r)
	}
	if got := r.ReadInt(); got != 0x1234 {
		t.Fatalf("stream misaligned after LOB: %#x", got)
	}
}

func TestUnknownTypeFlagsError(t *testing.T) {
	col := ColumnType{Code: 9999}
	w := NewRowOutput()
	w.WriteU8(1) // present
	r := NewRowInput(w.Bytes())
	_ = r.ReadValue(col)
	if r.Err() == nil {
		t.Fatal("expected error for unknown type code")
	}
}

func TestTwosComplementNegative(t *testing.T) {
	for _, s := range []string{"-1", "-128", "-129", "-255", "-256", "-32768", "-9223372036854775808"} {
		v, _ := new(big.Int).SetString(s, 10)
		enc := bigIntToTwosComplement(v)
		back := twosComplementToBigInt(enc)
		if back.Cmp(v) != 0 {
			t.Errorf("%s -> % x -> %s", s, enc, back)
		}
	}
}

func TestModifiedUTF8Surrogate(t *testing.T) {
	// An emoji is above U+FFFF: modified UTF-8 encodes it as a 6-byte surrogate
	// pair, distinct from standard UTF-8's 4 bytes.
	s := "a😀b"
	enc := encodeModifiedUTF8(s)
	if len(enc) != 1+6+1 {
		t.Fatalf("encoded length = %d, want 8", len(enc))
	}
	if got := decodeModifiedUTF8(enc); got != s {
		t.Fatalf("round-trip = %q", got)
	}
}

func TestShortBufferErrors(t *testing.T) {
	r := NewRowInput([]byte{0x00, 0x01}) // only 2 bytes
	_ = r.ReadInt()                      // wants 4
	if r.Err() == nil {
		t.Fatal("expected short-buffer error on ReadInt")
	}
	// Subsequent reads are no-ops returning zero values.
	if r.ReadLong() != 0 || r.ReadString() != "" {
		t.Fatal("reads after error should yield zero values")
	}
}

// --- Metadata + Result decode via hand-built payloads ---

// buildResultMetadata writes RESULT_METADATA for the given columns.
func buildResultMetadata(w *RowOutput, cols []Column) {
	w.WriteInt(MetaResult)
	w.WriteInt(int32(len(cols))) // columnCount
	w.WriteInt(int32(len(cols))) // extendedColumnCount (== columnCount)
	for _, c := range cols {
		w.WriteDataType(c.Type)
	}
	for _, c := range cols {
		w.WriteString(c.Label)
		w.WriteString(c.Catalog)
		w.WriteString(c.Schema)
		w.WriteString(c.Table)
		w.WriteString(c.Name)
		w.WriteU8(c.Attrs)
	}
}

func TestDecodeDataResult(t *testing.T) {
	cols := []Column{
		{Type: ColumnType{Code: SQLInteger}, Label: "id", Name: "ID", Table: "T"},
		{Type: ColumnType{Code: SQLVarchar}, Label: "name", Name: "NAME", Table: "T"},
	}
	rows := [][]any{
		{int64(1), "alice"},
		{int64(2), nil}, // NULL name
	}

	w := NewRowOutput()
	w.WriteLong(42) // result id
	w.WriteInt(0)   // updateCount
	w.WriteInt(0)   // fetchSize
	w.WriteU8(0)    // rsProperties
	buildResultMetadata(w, cols)
	// row block
	w.WriteLong(42)              // nav id
	w.WriteInt(int32(len(rows))) // total size
	w.WriteInt(0)                // current offset
	w.WriteInt(int32(len(rows))) // block length
	for _, row := range rows {
		for i, c := range cols {
			if err := w.WriteValue(c.Type, row[i]); err != nil {
				t.Fatalf("write value: %v", err)
			}
		}
	}

	res, err := DecodeResult(ModeData, w.Bytes())
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if res.ID != 42 {
		t.Errorf("id = %d", res.ID)
	}
	if res.Meta == nil || len(res.Meta.Columns) != 2 {
		t.Fatalf("meta columns = %+v", res.Meta)
	}
	if res.Meta.Columns[0].Label != "id" || res.Meta.Columns[1].Name != "NAME" {
		t.Errorf("labels/names wrong: %+v", res.Meta.Columns)
	}
	if res.RowSet == nil || len(res.RowSet.Rows) != 2 {
		t.Fatalf("rows = %+v", res.RowSet)
	}
	if res.RowSet.Rows[0][0] != int64(1) || res.RowSet.Rows[0][1] != "alice" {
		t.Errorf("row0 = %v", res.RowSet.Rows[0])
	}
	if res.RowSet.Rows[1][1] != nil {
		t.Errorf("row1 name should be nil, got %v", res.RowSet.Rows[1][1])
	}
}

func TestDecodeUpdateCountAndError(t *testing.T) {
	w := NewRowOutput()
	w.WriteInt(7)
	res, err := DecodeResult(ModeUpdateCount, w.Bytes())
	if err != nil || res.UpdateCount != 7 {
		t.Fatalf("update count decode: %v %+v", err, res)
	}

	w = NewRowOutput()
	w.WriteString("boom")
	w.WriteString("42S02")
	w.WriteInt(-5501)
	res, err = DecodeResult(ModeError, w.Bytes())
	if err != nil {
		t.Fatalf("error decode: %v", err)
	}
	if res.Message != "boom" || res.SQLState != "42S02" || res.ErrorCode != -5501 {
		t.Errorf("error fields = %+v", res)
	}
}

func TestDecodeUnknownModeFails(t *testing.T) {
	if _, err := DecodeResult(Mode(200), []byte{}); err == nil {
		t.Fatal("expected error decoding unknown mode")
	}
}

func TestEncodeRequestPayloads(t *testing.T) {
	// Each request mode should encode without error and be non-empty (except
	// the payload-less transaction control modes).
	modes := []*Result{
		{Mode: ModeConnect, DatabaseName: "db", User: "SA"},
		{Mode: ModeExecDirect, SQL: "SELECT 1"},
		{Mode: ModePrepare, SQL: "SELECT ?"},
		{Mode: ModeCloseResult, ID: 3},
		{Mode: ModeFreeStmt, StatementID: 9},
		{Mode: ModeRequestData, ID: 3, FetchSize: 10},
		{Mode: ModeEndTran, TxType: TxCommit},
		{Mode: ModeStartTran},
	}
	for _, req := range modes {
		w := NewRowOutput()
		if err := req.EncodePayload(w); err != nil {
			t.Errorf("mode %d: %v", req.Mode, err)
		}
	}

	// EXECUTE with a parameter uses the param metadata types.
	exec := &Result{
		Mode:        ModeExecute,
		StatementID: 1,
		ParamMeta:   &Metadata{ColumnCount: 1, Types: []ColumnType{{Code: SQLInteger}}},
		ParamValues: []any{int64(5)},
	}
	w := NewRowOutput()
	if err := exec.EncodePayload(w); err != nil {
		t.Fatalf("execute encode: %v", err)
	}

	// An unsupported request mode errors.
	if err := (&Result{Mode: ModeData}).EncodePayload(NewRowOutput()); err == nil {
		t.Fatal("expected error encoding response mode as request")
	}
}

func TestFrameInvalidLength(t *testing.T) {
	// A frame claiming length < 4 is malformed.
	buf := []byte{byte(ModeExecDirect), 0, 0, 0, 2} // length = 2
	br := bufio.NewReader(bytes.NewReader(buf))
	if _, _, err := ReadFrame(br); err == nil {
		t.Fatal("expected error for length < 4")
	}
}
