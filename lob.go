package hsql

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"unicode/utf16"

	"github.com/rennerdo30/go-hsql/internal/proto"
)

// maxLobChunk bounds a single GET_BYTES/GET_CHARS request so very large LOBs
// are fetched in pieces rather than one enormous allocation/round-trip.
const maxLobChunk = 1 << 20

// Blob is a streaming BLOB parameter. Use NewBlob to bind an io.Reader to a
// BLOB column without first materializing the whole value as []byte.
type Blob struct {
	Reader io.Reader
	Length int64 // bytes; negative means unknown length and uses segmented create
}

// NewBlob returns a streaming BLOB parameter. If length is negative, the reader
// is sent in chunks until EOF.
func NewBlob(r io.Reader, length int64) Blob {
	return Blob{Reader: r, Length: length}
}

// Clob is a streaming CLOB parameter. The reader must produce UTF-8 text.
type Clob struct {
	Reader io.Reader
	Length int64 // UTF-16 code units; negative means unknown length and uses segmented create
}

// NewClob returns a streaming CLOB parameter. If length is negative, the reader
// is decoded as UTF-8 and sent in chunks until EOF.
func NewClob(r io.Reader, length int64) Clob {
	return Clob{Reader: r, Length: length}
}

type pendingLob struct {
	id         int64
	isClob     bool
	bytes      []byte
	chars      []uint16
	blobReader io.Reader
	clobReader io.Reader
	length     int64
}

func (c *conn) nextLobID() int64 {
	id := c.lobIDSeq
	c.lobIDSeq--
	return id
}

// prepareLobParams converts CLOB/BLOB parameter payloads into temporary LOB
// references. The EXECUTE frame binds only the id; the payload is sent as
// LARGE_OBJECT_OP frames in the same transmission.
func (c *conn) prepareLobParams(req *proto.Result) ([]pendingLob, error) {
	if req.Mode != proto.ModeExecute || req.ParamMeta == nil {
		return nil, nil
	}
	n := int(req.ParamMeta.ColumnCount)
	if n == 0 {
		return nil, nil
	}
	var lobs []pendingLob
	for i := 0; i < n; i++ {
		if i >= len(req.ParamValues) || req.ParamValues[i] == nil {
			continue
		}
		switch req.ParamMeta.Types[i].Code {
		case proto.SQLBlob:
			if _, ok := req.ParamValues[i].(proto.LobRef); ok {
				continue
			}
			lob, err := c.blobParam(req.ParamValues[i])
			if err != nil {
				return nil, err
			}
			id := c.nextLobID()
			req.ParamValues[i] = proto.LobRef{ID: id}
			lob.id = id
			lobs = append(lobs, lob)
		case proto.SQLClob:
			if _, ok := req.ParamValues[i].(proto.LobRef); ok {
				continue
			}
			lob, err := c.clobParam(req.ParamValues[i])
			if err != nil {
				return nil, err
			}
			id := c.nextLobID()
			req.ParamValues[i] = proto.LobRef{ID: id, IsClob: true}
			lob.id = id
			lobs = append(lobs, lob)
		}
	}
	return lobs, nil
}

func (c *conn) blobParam(v any) (pendingLob, error) {
	switch x := v.(type) {
	case []byte:
		return pendingLob{bytes: x}, nil
	case string:
		return pendingLob{bytes: []byte(x)}, nil
	case Blob:
		if x.Reader == nil {
			return pendingLob{}, fmt.Errorf("hsql: nil BLOB reader")
		}
		return pendingLob{blobReader: x.Reader, length: x.Length}, nil
	case *Blob:
		if x == nil || x.Reader == nil {
			return pendingLob{}, fmt.Errorf("hsql: nil BLOB reader")
		}
		return pendingLob{blobReader: x.Reader, length: x.Length}, nil
	default:
		return pendingLob{}, fmt.Errorf("hsql: cannot bind %T as BLOB", v)
	}
}

func (c *conn) clobParam(v any) (pendingLob, error) {
	switch x := v.(type) {
	case string:
		return pendingLob{isClob: true, chars: utf16.Encode([]rune(x))}, nil
	case []byte:
		return pendingLob{isClob: true, chars: utf16.Encode([]rune(string(x)))}, nil
	case Clob:
		if x.Reader == nil {
			return pendingLob{}, fmt.Errorf("hsql: nil CLOB reader")
		}
		return pendingLob{isClob: true, clobReader: x.Reader, length: x.Length}, nil
	case *Clob:
		if x == nil || x.Reader == nil {
			return pendingLob{}, fmt.Errorf("hsql: nil CLOB reader")
		}
		return pendingLob{isClob: true, clobReader: x.Reader, length: x.Length}, nil
	default:
		return pendingLob{}, fmt.Errorf("hsql: cannot bind %T as CLOB", v)
	}
}

func (c *conn) writeLobCreate(lob pendingLob) error {
	if lob.isClob {
		if lob.clobReader != nil {
			return c.writeLobCreateCharsFromReader(lob)
		}
		return c.writeLobCharsFrame(proto.LobReqCreateChars, lob.id, 0, lob.chars)
	}
	if lob.blobReader != nil {
		return c.writeLobCreateBytesFromReader(lob)
	}
	return c.writeLobBytesFrame(proto.LobReqCreateBytes, lob.id, 0, lob.bytes)
}

func (c *conn) writeLobBytesFrame(subType int32, id, off int64, b []byte) error {
	c.writeLobHeader(subType, id)
	writeI64(c.bw, off)
	writeI64(c.bw, int64(len(b)))
	_, err := c.bw.Write(b)
	return err
}

func (c *conn) writeLobCharsFrame(subType int32, id, off int64, chars []uint16) error {
	c.writeLobHeader(subType, id)
	writeI64(c.bw, off)
	writeI64(c.bw, int64(len(chars)))
	return writeUTF16(c.bw, chars)
}

func (c *conn) writeLobCreateBytesFromReader(lob pendingLob) error {
	if lob.length >= 0 {
		c.writeLobHeader(proto.LobReqCreateBytes, lob.id)
		writeI64(c.bw, 0)
		writeI64(c.bw, lob.length)
		_, err := io.CopyN(c.bw, lob.blobReader, lob.length)
		return err
	}
	buf := make([]byte, maxLobChunk)
	off := int64(0)
	first := true
	for {
		n, err := lob.blobReader.Read(buf)
		if n > 0 {
			subType := proto.LobReqSetBytes
			if first {
				subType = proto.LobReqCreateBytes
				first = false
			}
			if werr := c.writeLobBytesFrame(subType, lob.id, off, buf[:n]); werr != nil {
				return werr
			}
			off += int64(n)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
	}
	if first {
		return c.writeLobBytesFrame(proto.LobReqCreateBytes, lob.id, 0, nil)
	}
	return nil
}

func (c *conn) writeLobCreateCharsFromReader(lob pendingLob) error {
	br := bufio.NewReader(lob.clobReader)
	off := int64(0)
	first := true
	for {
		chars, err := readUTF16Chunk(br, maxLobChunk)
		if len(chars) > 0 {
			subType := proto.LobReqSetChars
			if first {
				subType = proto.LobReqCreateChars
				first = false
			}
			if werr := c.writeLobCharsFrame(subType, lob.id, off, chars); werr != nil {
				return werr
			}
			off += int64(len(chars))
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
	}
	if first {
		return c.writeLobCharsFrame(proto.LobReqCreateChars, lob.id, 0, nil)
	}
	return nil
}

func readUTF16Chunk(r *bufio.Reader, maxUnits int) ([]uint16, error) {
	runes := make([]rune, 0, maxUnits)
	units := 0
	for units < maxUnits {
		ch, _, err := r.ReadRune()
		if err != nil {
			if err == io.EOF && len(runes) > 0 {
				return utf16.Encode(runes), nil
			}
			return nil, err
		}
		needed := 1
		if ch > 0xffff {
			needed = 2
		}
		if units+needed > maxUnits {
			if err := r.UnreadRune(); err != nil {
				return nil, err
			}
			break
		}
		runes = append(runes, ch)
		units += needed
	}
	return utf16.Encode(runes), nil
}

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

func writeUTF16(w interface{ Write([]byte) (int, error) }, chars []uint16) error {
	raw := make([]byte, len(chars)*2)
	for i, ch := range chars {
		binary.BigEndian.PutUint16(raw[i*2:], ch)
	}
	_, err := w.Write(raw)
	return err
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
