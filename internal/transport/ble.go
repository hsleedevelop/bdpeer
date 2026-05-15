package transport

import (
	"context"
	"fmt"
	"io"
	"sync"
)

// BLETransport implements Transport over BLE DataChar.
// Each peer gets an io.Pipe for inbound data and a sendFn for outbound.
// The sendFn is platform-provided (see internal/discovery/ble_darwin.go etc.).
type BLETransport struct {
	mu           sync.RWMutex
	entries      map[PeerID]*bleEntry
	handOnce     sync.Once
	handler      Handler
	handlerReady chan struct{}
}

type bleEntry struct {
	pw      *io.PipeWriter
	pr      *io.PipeReader
	sendFn  func([]byte)
	writeMu sync.Mutex
}

func NewBLETransport() *BLETransport {
	return &BLETransport{
		entries:      make(map[PeerID]*bleEntry),
		handlerReady: make(chan struct{}),
	}
}

func (t *BLETransport) Name() string { return "ble" }

// Attach registers a peer with its outbound send function.
// Replaces any existing entry for peer. Spawns the inbound reader goroutine.
func (t *BLETransport) Attach(peer PeerID, sendFn func([]byte)) {
	pr, pw := io.Pipe()
	e := &bleEntry{pr: pr, pw: pw, sendFn: sendFn}
	t.mu.Lock()
	if old, ok := t.entries[peer]; ok {
		old.pw.Close()
	}
	t.entries[peer] = e
	t.mu.Unlock()
	go t.runReader(peer, e)
}

// InboundData delivers raw frame bytes received from BLE to the peer's pipe.
// Called from the BLE data callback. The data slice is copied internally.
func (t *BLETransport) InboundData(peer PeerID, data []byte) {
	t.mu.RLock()
	e, ok := t.entries[peer]
	t.mu.RUnlock()
	if !ok {
		return
	}
	buf := make([]byte, len(data))
	copy(buf, data)
	e.pw.Write(buf)
}

// OpenStream implements Transport. Acquires the per-peer write mutex.
func (t *BLETransport) OpenStream(_ context.Context, peer PeerID) (io.ReadWriteCloser, error) {
	t.mu.RLock()
	e, ok := t.entries[peer]
	t.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNoConnection, peer)
	}
	e.writeMu.Lock()
	return &bleStream{entry: e}, nil
}

// SetHandler implements Transport.
func (t *BLETransport) SetHandler(h Handler) {
	t.handOnce.Do(func() {
		t.handler = h
		close(t.handlerReady)
	})
}

// Close implements Transport. Removes the peer entry and closes its pipe.
func (t *BLETransport) Close(peer PeerID) error {
	t.mu.Lock()
	e, ok := t.entries[peer]
	if ok {
		delete(t.entries, peer)
	}
	t.mu.Unlock()
	if !ok {
		return nil
	}
	e.pw.Close()
	return nil
}

func (t *BLETransport) runReader(peer PeerID, e *bleEntry) {
	<-t.handlerReady
	defer func() { recover() }()
	t.handler(peer, &bleReaderRWC{entry: e})
}

// bleStream is the per-call outbound wrapper. Write calls sendFn; Close releases writeMu.
type bleStream struct {
	entry     *bleEntry
	closeOnce sync.Once
}

func (s *bleStream) Read(_ []byte) (int, error)  { return 0, io.EOF }
func (s *bleStream) Write(p []byte) (int, error) {
	buf := make([]byte, len(p))
	copy(buf, p)
	s.entry.sendFn(buf)
	return len(p), nil
}
func (s *bleStream) Close() error {
	s.closeOnce.Do(func() { s.entry.writeMu.Unlock() })
	return nil
}

// bleReaderRWC is passed to the handler for reading inbound frames.
type bleReaderRWC struct {
	entry *bleEntry
}

func (r *bleReaderRWC) Read(p []byte) (int, error)  { return r.entry.pr.Read(p) }
func (r *bleReaderRWC) Write(p []byte) (int, error) { return len(p), nil }
func (r *bleReaderRWC) Close() error                { return nil }
