package hsql

import (
	"encoding/binary"
	"fmt"
	"io"
	"unicode/utf16"

	"github.com/rennerdo30/go-hsql/internal/proto"
)

// maxLobChunk bounds a single GET_BYTES/GET_CHARS request so very large LOBs
// are fetched in pieces rather than one enormous allocation/round-trip.
const maxLobChunk = 1 << 20

// fetchLob resolves a LOB reference to a []byte (BLOB) or string (CLOB) by
// querying its length and reading its content in chunks over the LOB
// sub-protocol.
func (c *conn) fetchLob(ref proto.LobRef) (any, error) {
	length, err := c.lobLength(ref.ID)
	if err != nil {
		return nil, err
	}
	if ref.IsClob {
		return c.lobGetChars(ref.ID, length)
	}
	return c.lobGetBytes(ref.ID, length)
}

// lobLength issues REQUEST_GET_LENGTH and returns the LOB's length (bytes for a
// BLOB, characters for a CLOB).
func (c *conn) lobLength(id int64) (int64, error) {
	resp, err := c.lobExchange(func() error {
		c.writeLobHeader(proto.LobReqGetLength, id)
		writeI64(c.bw, 0) // blockOffset
		return nil
	})
	if err != nil {
		return 0, err
	}
	return resp.blockLength, nil
}

func (c *conn) lobGetBytes(id, length int64) ([]byte, error) {
	out := make([]byte, 0, length)
	for off := int64(0); off < length; {
		n := length - off
		if n > maxLobChunk {
			n = maxLobChunk
		}
		resp, err := c.lobExchange(func() error {
			c.writeLobHeader(proto.LobReqGetBytes, id)
			writeI64(c.bw, off) // blockOffset
			writeI64(c.bw, n)   // blockLength
			return nil
		})
		if err != nil {
			return nil, err
		}
		if len(resp.byteBlock) == 0 {
			break
		}
		out = append(out, resp.byteBlock...)
		off += int64(len(resp.byteBlock))
	}
	return out, nil
}

func (c *conn) lobGetChars(id, length int64) (string, error) {
	units := make([]uint16, 0, length)
	for off := int64(0); off < length; {
		n := length - off
		if n > maxLobChunk {
			n = maxLobChunk
		}
		resp, err := c.lobExchange(func() error {
			c.writeLobHeader(proto.LobReqGetChars, id)
			writeI64(c.bw, off) // blockOffset (in chars)
			writeI64(c.bw, n)   // blockLength (in chars)
			return nil
		})
		if err != nil {
			return "", err
		}
		if len(resp.charBlock) == 0 {
			break
		}
		units = append(units, resp.charBlock...)
		off += int64(len(resp.charBlock))
	}
	return string(utf16.Decode(units)), nil
}

// lobResponse holds the decoded fields of a LOB response frame.
type lobResponse struct {
	subType     int32
	blockOffset int64
	blockLength int64
	byteBlock   []byte
	charBlock   []uint16
}

// lobExchange writes a LOB request (built by writeReq, which must emit the
// sub-type-specific fields after the header) plus the NONE terminator, flushes,
// and reads the response frame.
func (c *conn) lobExchange(writeReq func() error) (*lobResponse, error) {
	if c.closed || c.broken {
		return nil, fmt.Errorf("hsql: connection closed")
	}
	if err := writeReq(); err != nil {
		return nil, err
	}
	if err := c.bw.WriteByte(byte(proto.ModeNone)); err != nil {
		c.broken = true
		return nil, err
	}
	if err := c.bw.Flush(); err != nil {
		c.broken = true
		return nil, err
	}
	resp, err := c.readLobResponse()
	if err != nil {
		c.broken = true
		return nil, err
	}
	return resp, nil
}

// writeLobHeader writes the fixed LARGE_OBJECT_OP header.
func (c *conn) writeLobHeader(subType int32, id int64) {
	c.bw.WriteByte(byte(proto.ModeLargeObjectOp))
	writeI32(c.bw, c.databaseID)
	writeI64(c.bw, c.sessionID)
	writeI64(c.bw, id)
	writeI32(c.bw, subType)
}

// readLobResponse reads one response frame. A LOB error is returned by the
// server as a normal ERROR result; everything else is a LARGE_OBJECT_OP frame.
func (c *conn) readLobResponse() (*lobResponse, error) {
	mode, err := c.br.ReadByte()
	if err != nil {
		return nil, err
	}
	if proto.Mode(mode) == proto.ModeError {
		// Normal-framed ERROR result: length + payload.
		var lenBuf [4]byte
		if _, err := io.ReadFull(c.br, lenBuf[:]); err != nil {
			return nil, err
		}
		n := int(binary.BigEndian.Uint32(lenBuf[:])) - 4
		payload := make([]byte, n)
		if _, err := io.ReadFull(c.br, payload); err != nil {
			return nil, err
		}
		_, _ = c.br.ReadByte() // terminator
		res, derr := proto.DecodeResult(proto.ModeError, payload)
		if derr != nil {
			return nil, derr
		}
		return nil, errorFromResult(res)
	}
	if proto.Mode(mode) != proto.ModeLargeObjectOp {
		return nil, fmt.Errorf("hsql: unexpected LOB response mode %d", mode)
	}

	resp := &lobResponse{}
	_ = readI32(c.br) // databaseID
	_ = readI64(c.br) // sessionID
	_ = readI64(c.br) // lobID
	resp.subType = readI32(c.br)
	switch resp.subType {
	case proto.LobRespSet, proto.LobRespCreateBytes, proto.LobRespCreateChars, proto.LobRespTruncate:
		resp.blockLength = readI64(c.br)
	case proto.LobRespGetBytes:
		resp.blockOffset = readI64(c.br)
		resp.blockLength = readI64(c.br)
		resp.byteBlock = make([]byte, resp.blockLength)
		if _, err := io.ReadFull(c.br, resp.byteBlock); err != nil {
			return nil, err
		}
	case proto.LobRespGetChars:
		resp.blockOffset = readI64(c.br)
		resp.blockLength = readI64(c.br)
		resp.charBlock = make([]uint16, resp.blockLength)
		raw := make([]byte, resp.blockLength*2)
		if _, err := io.ReadFull(c.br, raw); err != nil {
			return nil, err
		}
		for i := range resp.charBlock {
			resp.charBlock[i] = binary.BigEndian.Uint16(raw[i*2:])
		}
	default:
		return nil, fmt.Errorf("hsql: unsupported LOB response sub-type %d", resp.subType)
	}
	_, _ = c.br.ReadByte() // NONE terminator
	return resp, nil
}

// --- big-endian stream helpers ---

func writeI32(w interface{ Write([]byte) (int, error) }, v int32) {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], uint32(v))
	_, _ = w.Write(b[:])
}

func writeI64(w interface{ Write([]byte) (int, error) }, v int64) {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], uint64(v))
	_, _ = w.Write(b[:])
}

func readI32(r io.Reader) int32 {
	var b [4]byte
	_, _ = io.ReadFull(r, b[:])
	return int32(binary.BigEndian.Uint32(b[:]))
}

func readI64(r io.Reader) int64 {
	var b [8]byte
	_, _ = io.ReadFull(r, b[:])
	return int64(binary.BigEndian.Uint64(b[:]))
}
