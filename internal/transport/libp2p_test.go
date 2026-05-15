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
	"github.com/libp2p/go-libp2p/core/peer"
)

// newHostPair creates two in-process libp2p hosts connected to each other.
func newHostPair(t *testing.T) (*bnet.Host, *bnet.Host, func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	hA, err := bnet.NewHost(ctx, "A")
	if err != nil {
		cancel()
		t.Fatalf("hostA: %v", err)
	}
	hB, err := bnet.NewHost(ctx, "B")
	if err != nil {
		hA.Close()
		cancel()
		t.Fatalf("hostB: %v", err)
	}
	piB := peer.AddrInfo{ID: hB.Libp2p.ID(), Addrs: hB.Libp2p.Addrs()}
	if err := hA.Libp2p.Connect(ctx, piB); err != nil {
		hA.Close()
		hB.Close()
		cancel()
		t.Fatalf("connect: %v", err)
	}
	return hA, hB, func() {
		hA.Close()
		hB.Close()
		cancel()
	}
}

func TestLibp2pTransport_OpenStreamAndRoundtrip(t *testing.T) {
	hA, hB, cleanup := newHostPair(t)
	defer cleanup()

	tA := NewLibp2pTransport(hA)
	tB := NewLibp2pTransport(hB)

	var wg sync.WaitGroup
	wg.Add(1)
	var gotFrame proto.Frame
	tB.SetHandler(func(_ PeerID, stream io.ReadWriteCloser) {
		defer stream.Close()
		f, err := transfer.ReadFrame(stream)
		if err != nil {
			t.Errorf("readFrame: %v", err)
			wg.Done()
			return
		}
		gotFrame = f
		wg.Done()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stream, err := tA.OpenStream(ctx, PeerID(hB.Libp2p.ID().String()))
	if err != nil {
		t.Fatalf("OpenStream: %v", err)
	}
	if err := transfer.WriteFrame(stream, proto.Frame{Type: proto.FrameText, From: "A", Content: "hello"}); err != nil {
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

	if gotFrame.Content != "hello" || gotFrame.From != "A" {
		t.Fatalf("unexpected frame: %+v", gotFrame)
	}
}

func TestLibp2pTransport_OpenStreamUnknownPeer(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h, err := bnet.NewHost(ctx, "solo")
	if err != nil {
		t.Fatalf("host: %v", err)
	}
	defer h.Close()

	tr := NewLibp2pTransport(h)
	// A peerID that doesn't exist.
	_, err = tr.OpenStream(ctx, PeerID("12D3KooWQYhTNQdmPWHRTwAFK6cZgSt4U4VkjqcXvgxBkRX5FfWy"))
	if err == nil {
		t.Fatalf("expected error for unknown peer")
	}
}
