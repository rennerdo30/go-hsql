package proto

import (
	"math"
	"math/big"
)

// RowOutput is an append-only big-endian byte buffer mirroring Java's
// DataOutputStream / org.hsqldb.rowio.RowOutputBinary. All integers are written
// big-endian (network byte order), matching Java's DataOutput.
type RowOutput struct {
	buf []byte
}

// NewRowOutput returns an empty RowOutput.
func NewRowOutput() *RowOutput { return &RowOutput{} }

// Reset truncates the buffer for reuse, keeping capacity.
func (w *RowOutput) Reset() { w.buf = w.buf[:0] }

// Bytes returns the accumulated bytes. The slice aliases the internal buffer.
func (w *RowOutput) Bytes() []byte { return w.buf }

// Len returns the number of bytes written so far.
func (w *RowOutput) Len() int { return len(w.buf) }

// WriteU8 appends a single byte.
func (w *RowOutput) WriteU8(b byte) { w.buf = append(w.buf, b) }

// WriteBool appends a boolean as a single 0/1 byte.
func (w *RowOutput) WriteBool(v bool) {
	if v {
		w.buf = append(w.buf, 1)
	} else {
		w.buf = append(w.buf, 0)
	}
}

// WriteShort appends a signed 16-bit big-endian integer.
func (w *RowOutput) WriteShort(v int16) {
	w.buf = append(w.buf, byte(v>>8), byte(v))
}

// WriteInt appends a signed 32-bit big-endian integer.
func (w *RowOutput) WriteInt(v int32) {
	w.buf = append(w.buf, byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
}

// WriteLong appends a signed 64-bit big-endian integer.
func (w *RowOutput) WriteLong(v int64) {
	w.buf = append(w.buf,
		byte(v>>56), byte(v>>48), byte(v>>40), byte(v>>32),
		byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
}

// WriteType appends a 2-byte SQL type code.
func (w *RowOutput) WriteType(t TypeCode) { w.WriteShort(int16(t)) }

// WriteDouble appends an IEEE-754 double as its 8-byte long bit pattern,
// matching Java's Double.doubleToLongBits over the wire.
func (w *RowOutput) WriteDouble(v float64) {
	w.WriteLong(int64(math.Float64bits(v)))
}

// WriteBytes appends raw bytes prefixed by their int32 length.
func (w *RowOutput) WriteBytes(b []byte) {
	w.WriteInt(int32(len(b)))
	w.buf = append(w.buf, b...)
}

// WriteRaw appends raw bytes with no length prefix.
func (w *RowOutput) WriteRaw(b []byte) { w.buf = append(w.buf, b...) }

// WriteString appends a string as int32 byte-length followed by its Java
// modified-UTF-8 encoding (org.hsqldb.lib.StringConverter.stringToUTFBytes).
// A nil/empty string writes length 0 with no bytes.
func (w *RowOutput) WriteString(s string) {
	enc := encodeModifiedUTF8(s)
	w.WriteBytes(enc)
}

// WriteDecimal appends a decimal as an unscaled two's-complement big.Int
// (length-prefixed) followed by the int32 scale, matching RowOutputBinary's
// writeDecimal / BigDecimal(unscaledValue, scale).
func (w *RowOutput) WriteDecimal(unscaled *big.Int, scale int32) {
	w.WriteBytes(bigIntToTwosComplement(unscaled))
	w.WriteInt(scale)
}

// PatchInt overwrites 4 bytes at position pos with a big-endian int32. Used to
// backfill a frame's length field once the payload size is known.
func (w *RowOutput) PatchInt(pos int, v int32) {
	w.buf[pos] = byte(v >> 24)
	w.buf[pos+1] = byte(v >> 16)
	w.buf[pos+2] = byte(v >> 8)
	w.buf[pos+3] = byte(v)
}

// encodeModifiedUTF8 encodes s using Java's modified UTF-8: U+0000 is written as
// 0xC0 0x80, code points above U+FFFF are written as a UTF-16 surrogate pair with
// each surrogate encoded as three bytes (CESU-8 style). ASCII is unchanged.
func encodeModifiedUTF8(s string) []byte {
	out := make([]byte, 0, len(s))
	for _, r := range s {
		switch {
		case r == 0x0000:
			out = append(out, 0xC0, 0x80)
		case r <= 0x007F:
			out = append(out, byte(r))
		case r <= 0x07FF:
			out = append(out, byte(0xC0|(r>>6)), byte(0x80|(r&0x3F)))
		case r <= 0xFFFF:
			out = append(out, byte(0xE0|(r>>12)), byte(0x80|((r>>6)&0x3F)), byte(0x80|(r&0x3F)))
		default:
			// Supplementary plane: split into a surrogate pair, encode each
			// surrogate as three bytes.
			r -= 0x10000
			hi := 0xD800 + (r >> 10)
			lo := 0xDC00 + (r & 0x3FF)
			out = append(out,
				byte(0xE0|(hi>>12)), byte(0x80|((hi>>6)&0x3F)), byte(0x80|(hi&0x3F)),
				byte(0xE0|(lo>>12)), byte(0x80|((lo>>6)&0x3F)), byte(0x80|(lo&0x3F)))
		}
	}
	return out
}

// bigIntToTwosComplement returns the minimal-length big-endian two's-complement
// representation of v, matching Java's BigInteger.toByteArray(). Zero yields a
// single 0x00 byte.
func bigIntToTwosComplement(v *big.Int) []byte {
	if v.Sign() == 0 {
		return []byte{0}
	}
	if v.Sign() > 0 {
		mag := v.Bytes()
		// Prepend a zero byte if the top bit is set, so the value reads as
		// positive in two's complement.
		if mag[0]&0x80 != 0 {
			return append([]byte{0}, mag...)
		}
		return mag
	}
	// Negative: compute two's complement of the magnitude over the minimal
	// number of bytes such that the sign bit is set.
	mag := new(big.Int).Neg(v).Bytes()
	n := len(mag)
	if mag[0]&0x80 != 0 {
		n++ // need an extra high byte for the sign
	}
	out := make([]byte, n)
	// Represent -v's magnitude, then negate: out = 2^(8n) - mag.
	mod := new(big.Int).Lsh(big.NewInt(1), uint(8*n))
	tc := new(big.Int).Sub(mod, new(big.Int).Neg(v))
	tcb := tc.Bytes()
	copy(out[n-len(tcb):], tcb)
	return out
}
