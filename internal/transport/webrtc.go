package transport

import (
	"context"
	"fmt"
	"io"
	"sync"

	bnet "github.com/hsleedevelop/bdpeer/internal/net"
)

// WebRTCTransport wraps a collection of *bnet.WebRTCConn keyed by PeerID.
// Connections are attached externally (by the discovery/upgrader layer) via
// Attach; the transport then owns their reader goroutines and exposes them
// through the Transport interface.
//
// PeerID values used with this transport are typically "ble-<uuid>" but the
// transport treats them as opaque keys.
type WebRTCTransport struct {
	mu           sync.RWMutex
	conns        map[PeerID]*webrtcEntry
	handOnce     sync.Once
	handler      Handler // written exactly once inside handOnce.Do
	handlerReady chan struct{}
}

type webrtcEntry struct {
	conn    *bnet.WebRTCConn
	writeMu sync.Mutex
}

// NewWebRTCTransport returns an empty transport.
func NewWebRTCTransport() *WebRTCTransport {
	return &WebRTCTransport{
		conns:        make(map[PeerID]*webrtcEntry),
		handlerReady: make(chan struct{}),
	}
}

// Name implements Transport.
func (t *WebRTCTransport) Name() string { return "webrtc" }

// Attach registers an already-connected WebRTCConn under peer and spawns a
// goroutine that delivers inbound frames to the registered handler. The reader
// goroutine blocks until SetHandler is called, so Attach and SetHandler may be
// called in any order. Replaces any existing entry for peer — typical use is
// one Attach per peer for the lifetime of that connection.
func (t *WebRTCTransport) Attach(peer PeerID, conn *bnet.WebRTCConn) {
	e := &webrtcEntry{conn: conn}
	t.mu.Lock()
	t.conns[peer] = e
	t.mu.Unlock()
	go t.runReader(peer, e)
}

// OpenStream implements Transport. Returns a per-call stream wrapper around
// the persistent WebRTCConn. The wrapper acquires the per-peer write mutex
// on creation and releases it on Close — concurrent OpenStream calls to the
// same peer serialize.
func (t *WebRTCTransport) OpenStream(_ context.Context, p PeerID) (io.ReadWriteCloser, error) {
	t.mu.RLock()
	e, ok := t.conns[p]
	t.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNoConnection, p)
	}
	e.writeMu.Lock()
	return &webrtcStream{entry: e}, nil
}

// SetHandler implements Transport. Registers the inbound stream callback.
// May be called before or after Attach — reader goroutines block on
// handlerReady and proceed once this method returns. Subsequent calls are
// no-ops (sync.Once contract).
func (t *WebRTCTransport) SetHandler(h Handler) {
	t.handOnce.Do(func() {
		t.handler = h
		close(t.handlerReady)
	})
}

// Close implements Transport. Removes the per-peer entry and closes its
// underlying WebRTCConn. The reader goroutine exits on the resulting EOF.
func (t *WebRTCTransport) Close(p PeerID) error {
	t.mu.Lock()
	e, ok := t.conns[p]
	if ok {
		delete(t.conns, p)
	}
	t.mu.Unlock()
	if !ok {
		return nil
	}
	return e.conn.Close()
}

// runReader hands the persistent WebRTCConn to the registered handler.
// It blocks on handlerReady until SetHandler is called, then invokes the
// handler. If the transport is closed before SetHandler is called the conn
// will yield EOF and the goroutine exits cleanly after unblocking.
// The handler must not Close() the underlying *bnet.WebRTCConn — we pass a
// noCloseRWC wrapper to make that explicit.
func (t *WebRTCTransport) runReader(peer PeerID, e *webrtcEntry) {
	<-t.handlerReady
	defer func() {
		if r := recover(); r != nil {
			_ = r // handler panic must not kill transport
		}
	}()
	t.handler(peer, &noCloseRWC{rwc: e.conn})
}

// webrtcStream is the per-call outbound wrapper. Writes go to the underlying
// DataChannel; Close releases the per-peer write mutex.
type webrtcStream struct {
	entry     *webrtcEntry
	closeOnce sync.Once
}

func (s *webrtcStream) Read(_ []byte) (int, error) {
	// Outbound streams in this transport are write-only. Reads happen via
	// the inbound reader goroutine that runReader hands to the handler.
	return 0, io.EOF
}

func (s *webrtcStream) Write(p []byte) (int, error) {
	return s.entry.conn.Write(p)
}

func (s *webrtcStream) Close() error {
	s.closeOnce.Do(func() {
		s.entry.writeMu.Unlock()
	})
	return nil
}

// noCloseRWC prevents the handler from closing the underlying conn — the
// transport owns the conn lifetime and uses Close(peer) to tear it down.
type noCloseRWC struct {
	rwc io.ReadWriteCloser
}

func (n *noCloseRWC) Read(p []byte) (int, error)  { return n.rwc.Read(p) }
func (n *noCloseRWC) Write(p []byte) (int, error) { return n.rwc.Write(p) }
func (n *noCloseRWC) Close() error                { return nil }
