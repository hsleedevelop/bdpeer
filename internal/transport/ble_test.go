package transport

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/hsleedevelop/bdpeer/internal/proto"
)

func encodeFrame(t *testing.T, f proto.Frame) []byte {
	t.Helper()
	payload, _ := json.Marshal(f)
	buf := make([]byte, 4+len(payload))
	binary.BigEndian.PutUint32(buf[:4], uint32(len(payload)))
	copy(buf[4:], payload)
	return buf
}

func TestBLETransportRoundtrip(t *testing.T) {
	bt := NewBLETransport()

	var sent []byte
	sendFn := func(data []byte) {
		sent = append(sent, data...)
	}

	peer := PeerID("ble-test-peer")
	bt.Attach(peer, sendFn)

	received := make(chan proto.Frame, 1)
	bt.SetHandler(func(from PeerID, stream io.ReadWriteCloser) {
		var length uint32
		binary.Read(stream, binary.BigEndian, &length)
		buf := make([]byte, length)
		io.ReadFull(stream, buf)
		var f proto.Frame
		json.Unmarshal(buf, &f)
		received <- f
	})

	// Simulate inbound data arriving from BLE callback.
	inbound := encodeFrame(t, proto.Frame{Type: proto.FrameText, From: "alice", Content: "hi"})
	bt.InboundData(peer, inbound)

	select {
	case f := <-received:
		if f.Content != "hi" {
			t.Fatalf("want 'hi', got %q", f.Content)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for inbound frame")
	}

	// Simulate outbound write.
	ctx := context.Background()
	stream, err := bt.OpenStream(ctx, peer)
	if err != nil {
		t.Fatal(err)
	}
	outbound := encodeFrame(t, proto.Frame{Type: proto.FrameText, From: "bob", Content: "hello"})
	stream.Write(outbound)
	stream.Close()

	if !bytes.Equal(sent, outbound) {
		t.Fatalf("outbound data mismatch: want %v, got %v", outbound, sent)
	}
}

func TestBLETransportCoalescesSplitFrameWrites(t *testing.T) {
	bt := NewBLETransport()

	var sends [][]byte
	peer := PeerID("ble-test-peer")
	bt.Attach(peer, func(data []byte) {
		cp := make([]byte, len(data))
		copy(cp, data)
		sends = append(sends, cp)
	})
	bt.SetHandler(func(_ PeerID, _ io.ReadWriteCloser) {})

	outbound := encodeFrame(t, proto.Frame{Type: proto.FrameText, From: "bob", Content: "hello"})
	stream, err := bt.OpenStream(context.Background(), peer)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Write(outbound[:4]); err != nil {
		t.Fatal(err)
	}
	if len(sends) != 0 {
		t.Fatalf("length prefix alone should not be sent, got %d sends", len(sends))
	}
	if _, err := stream.Write(outbound[4:]); err != nil {
		t.Fatal(err)
	}
	stream.Close()

	if len(sends) != 1 {
		t.Fatalf("want one coalesced send, got %d", len(sends))
	}
	if !bytes.Equal(sends[0], outbound) {
		t.Fatalf("coalesced data mismatch: want %v, got %v", outbound, sends[0])
	}
}

func TestBLETransportNoConnection(t *testing.T) {
	bt := NewBLETransport()
	bt.SetHandler(func(_ PeerID, _ io.ReadWriteCloser) {})
	_, err := bt.OpenStream(context.Background(), PeerID("ghost"))
	if !errors.Is(err, ErrNoConnection) {
		t.Fatalf("expected ErrNoConnection, got %v", err)
	}
}

func TestBLETransportClose(t *testing.T) {
	bt := NewBLETransport()
	bt.SetHandler(func(_ PeerID, _ io.ReadWriteCloser) {})

	peer := PeerID("ble-close-test")
	bt.Attach(peer, func(_ []byte) {})
	bt.Close(peer)

	_, err := bt.OpenStream(context.Background(), peer)
	if err == nil {
		t.Fatal("expected error after close")
	}
}
