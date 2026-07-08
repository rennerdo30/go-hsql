package proto

import (
	"errors"
	"math"
	"math/big"
)

// ErrShortBuffer is returned (via RowInput.Err) when a read runs past the end
// of the buffer.
var ErrShortBuffer = errors.New("hsql/proto: unexpected end of buffer")

// RowInput reads big-endian values from a byte slice, mirroring Java's
// DataInputStream / org.hsqldb.rowio.RowInputBinary. Read methods do not return
// an error individually; after a sequence of reads, check Err. Once an error is
// set, subsequent reads return zero values.
type RowInput struct {
	buf []byte
	pos int
	err error
}

// NewRowInput wraps buf for reading.
func NewRowInput(buf []byte) *RowInput { return &RowInput{buf: buf} }

// Err returns the first error encountered, if any.
func (r *RowInput) Err() error { return r.err }

// Remaining returns the number of unread bytes.
func (r *RowInput) Remaining() int { return len(r.buf) - r.pos }

func (r *RowInput) need(n int) bool {
	if r.err != nil {
		return false
	}
	if r.pos+n > len(r.buf) {
		r.err = ErrShortBuffer
		return false
	}
	return true
}

// ReadU8 reads a single unsigned byte.
func (r *RowInput) ReadU8() byte {
	if !r.need(1) {
		return 0
	}
	b := r.buf[r.pos]
	r.pos++
	return b
}

// ReadBool reads a single 0/1 byte as a boolean.
func (r *RowInput) ReadBool() bool { return r.ReadU8() != 0 }

// ReadShort reads a signed 16-bit big-endian integer.
func (r *RowInput) ReadShort() int16 {
	if !r.need(2) {
		return 0
	}
	v := int16(r.buf[r.pos])<<8 | int16(r.buf[r.pos+1])
	r.pos += 2
	return v
}

// ReadInt reads a signed 32-bit big-endian integer.
func (r *RowInput) ReadInt() int32 {
	if !r.need(4) {
		return 0
	}
	v := int32(r.buf[r.pos])<<24 | int32(r.buf[r.pos+1])<<16 |
		int32(r.buf[r.pos+2])<<8 | int32(r.buf[r.pos+3])
	r.pos += 4
	return v
}

// ReadLong reads a signed 64-bit big-endian integer.
func (r *RowInput) ReadLong() int64 {
	if !r.need(8) {
		return 0
	}
	b := r.buf[r.pos : r.pos+8]
	v := int64(b[0])<<56 | int64(b[1])<<48 | int64(b[2])<<40 | int64(b[3])<<32 |
		int64(b[4])<<24 | int64(b[5])<<16 | int64(b[6])<<8 | int64(b[7])
	r.pos += 8
	return v
}

// ReadType reads a 2-byte SQL type code.
func (r *RowInput) ReadType() TypeCode { return TypeCode(r.ReadShort()) }

// ReadDouble reads an 8-byte IEEE-754 double from its long bit pattern.
func (r *RowInput) ReadDouble() float64 { return math.Float64frombits(uint64(r.ReadLong())) }

// ReadBytes reads an int32-length-prefixed raw byte slice. The returned slice
// is a copy.
func (r *RowInput) ReadBytes() []byte {
	n := int(r.ReadInt())
	if r.err != nil || n < 0 {
		return nil
	}
	if !r.need(n) {
		return nil
	}
	out := make([]byte, n)
	copy(out, r.buf[r.pos:r.pos+n])
	r.pos += n
	return out
}

// ReadString reads an int32-length-prefixed Java modified-UTF-8 string.
func (r *RowInput) ReadString() string {
	n := int(r.ReadInt())
	if r.err != nil || n < 0 {
		return ""
	}
	if !r.need(n) {
		return ""
	}
	s := decodeModifiedUTF8(r.buf[r.pos : r.pos+n])
	r.pos += n
	return s
}

// ReadDecimal reads an unscaled two's-complement big.Int (length-prefixed)
// followed by an int32 scale, returning the unscaled value and scale.
func (r *RowInput) ReadDecimal() (unscaled *big.Int, scale int32) {
	b := r.ReadBytes()
	scale = r.ReadInt()
	return twosComplementToBigInt(b), scale
}

// decodeModifiedUTF8 decodes Java modified UTF-8 back to a Go string. Malformed
// sequences are passed through best-effort; well-formed HSQLDB output round-trips
// exactly with encodeModifiedUTF8.
func decodeModifiedUTF8(b []byte) string {
	out := make([]rune, 0, len(b))
	i := 0
	for i < len(b) {
		c := b[i]
		switch {
		case c < 0x80:
			out = append(out, rune(c))
			i++
		case c&0xE0 == 0xC0:
			if i+1 >= len(b) {
				out = append(out, rune(c))
				i++
				continue
			}
			r := rune(c&0x1F)<<6 | rune(b[i+1]&0x3F)
			out = append(out, r)
			i += 2
		case c&0xF0 == 0xE0:
			if i+2 >= len(b) {
				out = append(out, rune(c))
				i++
				continue
			}
			r := rune(c&0x0F)<<12 | rune(b[i+1]&0x3F)<<6 | rune(b[i+2]&0x3F)
			// Recombine a surrogate pair encoded as two 3-byte sequences.
			if r >= 0xD800 && r <= 0xDBFF && i+5 < len(b) &&
				b[i+3]&0xF0 == 0xE0 {
				lo := rune(b[i+3]&0x0F)<<12 | rune(b[i+4]&0x3F)<<6 | rune(b[i+5]&0x3F)
				if lo >= 0xDC00 && lo <= 0xDFFF {
					r = 0x10000 + (r-0xD800)<<10 + (lo - 0xDC00)
					out = append(out, r)
					i += 6
					continue
				}
			}
			out = append(out, r)
			i += 3
		default:
			out = append(out, rune(c))
			i++
		}
	}
	return string(out)
}

// twosComplementToBigInt reverses bigIntToTwosComplement, decoding a big-endian
// two's-complement byte slice (Java BigInteger.toByteArray form) into a big.Int.
func twosComplementToBigInt(b []byte) *big.Int {
	if len(b) == 0 {
		return big.NewInt(0)
	}
	v := new(big.Int).SetBytes(b)
	if b[0]&0x80 != 0 {
		// Negative: subtract 2^(8*len).
		mod := new(big.Int).Lsh(big.NewInt(1), uint(8*len(b)))
		v.Sub(v, mod)
	}
	return v
}
