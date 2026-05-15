package transport

import (
	"context"
	"io"
	"sync"
	"testing"
	"time"

	bnet "github.com/hsleedevelop/bdpeer/internal/net"
	"github.com/hsleedevelop/bdpeer/internal/proto"
	"github.com/hsleedevelop/bdpeer/internal/transfer"
)

// newWebRTCPair creates two paired *bnet.WebRTCConn via direct SDP exchange
// (bypasses BLE). Returns (connA, connB).
func newWebRTCPair(t *testing.T) (*bnet.WebRTCConn, *bnet.WebRTCConn) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	connA, offerSDP, err := bnet.NewWebRTCOffer(ctx, nil)
	if err != nil {
		t.Fatalf("NewWebRTCOffer: %v", err)
	}
	connB, answerSDP, err := bnet.NewWebRTCAnswer(ctx, offerSDP, nil)
	if err != nil {
		connA.Close()
		t.Fatalf("NewWebRTCAnswer: %v", err)
	}
	if err := connA.SetAnswer(answerSDP); err != nil {
		connA.Close()
		connB.Close()
		t.Fatalf("SetAnswer: %v", err)
	}

	// Wait for both sides to reach Connected state and data channel open.
	// Connected() fires on ICE/DTLS; DataChannelOpen() fires after SCTP handshake.
	// Writes must not be attempted until DataChannelOpen() fires.
	select {
	case <-connA.DataChannelOpen():
	case <-time.After(10 * time.Second):
		connA.Close()
		connB.Close()
		t.Fatalf("connA DataChannelOpen timeout")
	}
	select {
	case <-connB.DataChannelOpen():
	case <-time.After(10 * time.Second):
		connA.Close()
		connB.Close()
		t.Fatalf("connB DataChannelOpen timeout")
	}
	return connA, connB
}

func TestWebRTCTransport_OpenStreamAndInbound(t *testing.T) {
	connA, connB := newWebRTCPair(t)
	defer connA.Close()
	defer connB.Close()

	tA := NewWebRTCTransport()
	tB := NewWebRTCTransport()

	var wg sync.WaitGroup
	wg.Add(1)
	var gotFrame proto.Frame
	tB.SetHandler(func(_ PeerID, stream io.ReadWriteCloser) {
		// Read one frame from the stream and signal.
		f, err := transfer.ReadFrame(stream)
		if err != nil {
			t.Errorf("readFrame: %v", err)
			wg.Done()
			return
		}
		gotFrame = f
		wg.Done()
	})

	tA.Attach("peerB", connA)
	tB.Attach("peerA", connB)

	ctx := context.Background()
	stream, err := tA.OpenStream(ctx, "peerB")
	if err != nil {
		t.Fatalf("OpenStream: %v", err)
	}
	if err := transfer.WriteFrame(stream, proto.Frame{Type: proto.FrameText, From: "A", Content: "hi"}); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}
	stream.Close()

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("timeout waiting for inbound frame")
	}
	if gotFrame.Content != "hi" || gotFrame.From != "A" {
		t.Fatalf("unexpected frame: %+v", gotFrame)
	}
}

func TestWebRTCTransport_OpenStreamUnknownPeer(t *testing.T) {
	tr := NewWebRTCTransport()
	_, err := tr.OpenStream(context.Background(), "ghost")
	if err == nil {
		t.Fatalf("expected error for unknown peer")
	}
}

func TestWebRTCTransport_MultipleMessagesAfterHandlerReturn(t *testing.T) {
	connA, connB := newWebRTCPair(t)
	defer connA.Close()
	defer connB.Close()

	tA := NewWebRTCTransport()
	tB := NewWebRTCTransport()

	received := make(chan proto.Frame, 4)

	// Loop-style handler that reads frames in a loop (mimics Service.onInboundStream).
	tB.SetHandler(func(_ PeerID, stream io.ReadWriteCloser) {
		for {
			f, err := transfer.ReadFrame(stream)
			if err != nil {
				return
			}
			received <- f
		}
	})

	tA.Attach("peerB", connA)
	tB.Attach("peerA", connB)

	// Send two text frames in succession; both must be received.
	for _, content := range []string{"one", "two"} {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		stream, err := tA.OpenStream(ctx, "peerB")
		cancel()
		if err != nil {
			t.Fatalf("OpenStream: %v", err)
		}
		if err := transfer.WriteFrame(stream, proto.Frame{Type: proto.FrameText, From: "A", Content: content}); err != nil {
			t.Fatalf("WriteFrame: %v", err)
		}
		stream.Close()
	}

	timeout := time.After(5 * time.Second)
	var got []string
	for i := 0; i < 2; i++ {
		select {
		case f := <-received:
			got = append(got, f.Content)
		case <-timeout:
			t.Fatalf("timeout: received %d/2 frames so far (%v)", i, got)
		}
	}
	if got[0] != "one" || got[1] != "two" {
		t.Fatalf("unexpected order/content: %v", got)
	}
}

func TestWebRTCTransport_OpenStreamSerializesPerPeer(t *testing.T) {
	connA, connB := newWebRTCPair(t)
	defer connA.Close()
	defer connB.Close()

	tA := NewWebRTCTransport()
	tA.Attach("peerB", connA)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// First OpenStream acquires the per-peer write lock.
	s1, err := tA.OpenStream(ctx, "peerB")
	if err != nil {
		t.Fatalf("first OpenStream: %v", err)
	}

	// Second OpenStream from another goroutine must block until s1.Close().
	gotSecond := make(chan error, 1)
	go func() {
		s2, err := tA.OpenStream(ctx, "peerB")
		if err == nil {
			s2.Close()
		}
		gotSecond <- err
	}()

	// Brief sleep to give the goroutine a chance to attempt OpenStream.
	time.Sleep(100 * time.Millisecond)
	select {
	case <-gotSecond:
		t.Fatalf("second OpenStream did not block while s1 held the lock")
	default:
	}

	// Releasing s1 should unblock the second call.
	s1.Close()
	select {
	case err := <-gotSecond:
		if err != nil {
			t.Fatalf("second OpenStream after release: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("second OpenStream did not unblock")
	}
}
