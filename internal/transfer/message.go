package transfer

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"

	"github.com/chad/bdpeer/internal/proto"
)

// WriteFrame encodes a Frame as: [4-byte big-endian length][JSON payload]
func WriteFrame(w io.Writer, f proto.Frame) error {
	payload, err := json.Marshal(f)
	if err != nil {
		return fmt.Errorf("marshal frame: %w", err)
	}
	length := uint32(len(payload))
	if err := binary.Write(w, binary.BigEndian, length); err != nil {
		return fmt.Errorf("write length: %w", err)
	}
	_, err = w.Write(payload)
	return err
}

// ReadFrame reads one Frame from r.
func ReadFrame(r io.Reader) (proto.Frame, error) {
	var length uint32
	if err := binary.Read(r, binary.BigEndian, &length); err != nil {
		return proto.Frame{}, fmt.Errorf("read length: %w", err)
	}
	if length > 64*1024*1024 {
		return proto.Frame{}, fmt.Errorf("frame too large: %d bytes", length)
	}
	buf := make([]byte, length)
	if _, err := io.ReadFull(r, buf); err != nil {
		return proto.Frame{}, fmt.Errorf("read payload: %w", err)
	}
	var f proto.Frame
	if err := json.Unmarshal(buf, &f); err != nil {
		return proto.Frame{}, fmt.Errorf("unmarshal frame: %w", err)
	}
	return f, nil
}
