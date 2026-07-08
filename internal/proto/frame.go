package proto

import (
	"bufio"
	"fmt"
	"io"
)

// WriteFrame writes a single Result frame: [mode:1][length:int32][payload].
// length = 4 + len(payload) (it counts itself, not the mode byte). It does not
// write the trailing NONE terminator; callers writing a complete transmission
// must follow the final frame with WriteTerminator.
func WriteFrame(w *bufio.Writer, mode Mode, payload []byte) error {
	if err := w.WriteByte(byte(mode)); err != nil {
		return err
	}
	length := int32(4 + len(payload))
	var hdr [4]byte
	hdr[0] = byte(length >> 24)
	hdr[1] = byte(length >> 16)
	hdr[2] = byte(length >> 8)
	hdr[3] = byte(length)
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	if _, err := w.Write(payload); err != nil {
		return err
	}
	return nil
}

// WriteTerminator writes the single NONE (0x00) byte that ends a transmission.
func WriteTerminator(w *bufio.Writer) error { return w.WriteByte(byte(ModeNone)) }

// ReadFrame reads one frame. If the leading byte is NONE (the transmission
// terminator) it returns mode == ModeNone and a nil payload. Otherwise it reads
// the int32 length and the length-4 payload bytes.
func ReadFrame(r *bufio.Reader) (Mode, []byte, error) {
	modeByte, err := r.ReadByte()
	if err != nil {
		return 0, nil, err
	}
	mode := Mode(modeByte)
	if mode == ModeNone {
		return ModeNone, nil, nil
	}
	length, err := readInt32(r)
	if err != nil {
		return mode, nil, err
	}
	if length < 4 {
		return mode, nil, fmt.Errorf("hsql/proto: invalid frame length %d for mode %d", length, mode)
	}
	payload := make([]byte, length-4)
	if _, err := io.ReadFull(r, payload); err != nil {
		return mode, nil, err
	}
	return mode, payload, nil
}

func readInt32(r *bufio.Reader) (int32, error) {
	var b [4]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return 0, err
	}
	return int32(b[0])<<24 | int32(b[1])<<16 | int32(b[2])<<8 | int32(b[3]), nil
}
