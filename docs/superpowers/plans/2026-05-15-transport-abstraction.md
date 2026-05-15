# Transport Abstraction (Phase 1) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Introduce a `Transport` interface and `Registry` in `internal/transport/`, wrap the existing libp2p and BLE-signaled WebRTC backends as `Transport` implementations, and replace `Service.Send`'s `"ble-"` prefix branch with Registry-based routing. No external behavior changes.

**Architecture:** New package `internal/transport/` defines `Transport` interface (`OpenStream`, `SetHandler`, `Close`, `Name`) and a `Registry` (`PeerID → Transport`). Two adapters wrap `*bnet.Host` and `*bnet.WebRTCConn`. Discovery layer registers peers as their underlying connections are established. `Service.Send` becomes Registry lookup + `OpenStream` + write + close.

**Tech Stack:** Go 1.x, libp2p (`github.com/libp2p/go-libp2p`), pion/webrtc v4, existing `internal/transfer` framing, existing `internal/proto` types.

**Reference spec:** [docs/superpowers/specs/2026-05-15-transport-abstraction-design.md](../specs/2026-05-15-transport-abstraction-design.md)

**Conventions for all tasks:**
- Working directory: `/Users/chad/Projects/workspace/bdpeer`
- Build command (default): `go build ./...`
- BLE build command (darwin): `go build ./... && go vet ./...` — BLE is included by default on darwin (no `-tags ble` needed since v0.3.1)
- Test command: `go test ./...`
- Race test command: `go test -race ./internal/transport/...`
- Commit each task at its end. Commit messages in Korean per repo convention; technical identifiers stay English.

---

### Task 1: Create transport package skeleton (PeerID, errors, Registry)

**Files:**
- Create: `internal/transport/transport.go`
- Create: `internal/transport/registry_test.go`

- [ ] **Step 1: Write the failing test for Registry basic operations**

Create `internal/transport/registry_test.go`:

```go
package transport

import (
	"testing"
)

type fakeTransport struct{ name string }

func (f *fakeTransport) OpenStream(_ contextStub, _ PeerID) (rwcStub, error) { return nil, nil }
func (f *fakeTransport) SetHandler(_ Handler)                                 {}
func (f *fakeTransport) Close(_ PeerID) error                                 { return nil }
func (f *fakeTransport) Name() string                                         { return f.name }

func TestRegistry_RegisterAndLookup(t *testing.T) {
	r := NewRegistry()
	tA := &fakeTransport{name: "A"}
	r.Register("peer1", tA)

	got, ok := r.Lookup("peer1")
	if !ok {
		t.Fatalf("expected peer1 to be registered")
	}
	if got.Name() != "A" {
		t.Fatalf("expected transport A, got %s", got.Name())
	}
}

func TestRegistry_Unregister(t *testing.T) {
	r := NewRegistry()
	r.Register("peer1", &fakeTransport{name: "A"})
	r.Unregister("peer1")
	if _, ok := r.Lookup("peer1"); ok {
		t.Fatalf("expected peer1 to be unregistered")
	}
}

func TestRegistry_RegisterOverwrites(t *testing.T) {
	r := NewRegistry()
	r.Register("peer1", &fakeTransport{name: "A"})
	r.Register("peer1", &fakeTransport{name: "B"})
	got, _ := r.Lookup("peer1")
	if got.Name() != "B" {
		t.Fatalf("expected overwrite to B, got %s", got.Name())
	}
}

func TestRegistry_LookupMissing(t *testing.T) {
	r := NewRegistry()
	if _, ok := r.Lookup("nope"); ok {
		t.Fatalf("expected miss")
	}
}
```

Note: `contextStub` and `rwcStub` are placeholder type names used only so this test compiles before the interface is finalized. We will replace them with the real types in Step 3. (Actually — better, define the real signatures upfront.) **Replace the test with the version below before saving:**

```go
package transport

import (
	"context"
	"io"
	"testing"
)

type fakeTransport struct{ name string }

func (f *fakeTransport) OpenStream(_ context.Context, _ PeerID) (io.ReadWriteCloser, error) {
	return nil, nil
}
func (f *fakeTransport) SetHandler(_ Handler) {}
func (f *fakeTransport) Close(_ PeerID) error { return nil }
func (f *fakeTransport) Name() string         { return f.name }

func TestRegistry_RegisterAndLookup(t *testing.T) {
	r := NewRegistry()
	tA := &fakeTransport{name: "A"}
	r.Register("peer1", tA)

	got, ok := r.Lookup("peer1")
	if !ok {
		t.Fatalf("expected peer1 to be registered")
	}
	if got.Name() != "A" {
		t.Fatalf("expected transport A, got %s", got.Name())
	}
}

func TestRegistry_Unregister(t *testing.T) {
	r := NewRegistry()
	r.Register("peer1", &fakeTransport{name: "A"})
	r.Unregister("peer1")
	if _, ok := r.Lookup("peer1"); ok {
		t.Fatalf("expected peer1 to be unregistered")
	}
}

func TestRegistry_RegisterOverwrites(t *testing.T) {
	r := NewRegistry()
	r.Register("peer1", &fakeTransport{name: "A"})
	r.Register("peer1", &fakeTransport{name: "B"})
	got, _ := r.Lookup("peer1")
	if got.Name() != "B" {
		t.Fatalf("expected overwrite to B, got %s", got.Name())
	}
}

func TestRegistry_LookupMissing(t *testing.T) {
	r := NewRegistry()
	if _, ok := r.Lookup("nope"); ok {
		t.Fatalf("expected miss")
	}
}
```

- [ ] **Step 2: Run test to verify it fails (compile error — package doesn't exist)**

Run: `go test ./internal/transport/...`
Expected: FAIL with `no Go files in .../internal/transport` or compile errors referencing `NewRegistry`, `PeerID`, `Handler`, `Transport`.

- [ ] **Step 3: Implement the transport package skeleton**

Create `internal/transport/transport.go`:

```go
// Package transport defines a backend-agnostic abstraction for sending and
// receiving bdpeer protocol frames between peers. Concrete implementations
// wrap libp2p, BLE-signaled WebRTC, and (in future phases) BLE data channels.
package transport

import (
	"context"
	"errors"
	"io"
	"sync"
)

// PeerID is an opaque identifier for a peer. The concrete value depends on
// the transport that registered it: libp2p uses peer.ID.String(), the
// BLE-signaled WebRTC transport uses "ble-<uuid>". Routing is done via
// Registry — callers must not parse PeerID for transport hints.
type PeerID string

// Handler is invoked by a Transport for each inbound logical stream from a
// peer. The transport calls Handler in its own goroutine. The handler owns
// the stream and is responsible for closing it. The handler should not block
// the transport's internal state machine indefinitely; long reads are fine.
type Handler func(from PeerID, stream io.ReadWriteCloser)

// Transport is one connectivity backend.
type Transport interface {
	// OpenStream opens a new outbound stream to peer. The caller writes the
	// payload and Close()s. Returns ErrNoConnection if no underlying
	// connection is registered for peer at call time.
	OpenStream(ctx context.Context, peer PeerID) (io.ReadWriteCloser, error)

	// SetHandler registers the inbound stream callback. Implementations must
	// support calling this once at startup; later calls replace the handler.
	SetHandler(h Handler)

	// Close tears down per-peer state. Does not stop the transport itself.
	// For libp2p this is typically a no-op (host owns connections). For
	// WebRTC this releases the per-peer connection and reader goroutine.
	Close(peer PeerID) error

	// Name returns a short identifier ("libp2p", "webrtc") for logging.
	Name() string
}

// ErrNoConnection is returned by OpenStream when the underlying connection
// for the peer is not (or no longer) available. Callers should treat this
// as a signal to Unregister the peer from the Registry.
var ErrNoConnection = errors.New("transport: no connection for peer")

// Registry routes PeerID → Transport. It is concurrent-safe.
//
// Registering an already-present peer overwrites the previous transport.
// This is intentional: Phase 3 fallback will replace a peer's WebRTC
// transport with a BLE data transport in place.
type Registry struct {
	mu    sync.RWMutex
	peers map[PeerID]Transport
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{peers: make(map[PeerID]Transport)}
}

// Register associates peer with t. Overwrites any existing entry.
func (r *Registry) Register(peer PeerID, t Transport) {
	r.mu.Lock()
	r.peers[peer] = t
	r.mu.Unlock()
}

// Unregister removes peer from the registry. No-op if absent.
func (r *Registry) Unregister(peer PeerID) {
	r.mu.Lock()
	delete(r.peers, peer)
	r.mu.Unlock()
}

// Lookup returns the transport currently associated with peer.
func (r *Registry) Lookup(peer PeerID) (Transport, bool) {
	r.mu.RLock()
	t, ok := r.peers[peer]
	r.mu.RUnlock()
	return t, ok
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/transport/...`
Expected: PASS — 4 tests OK.

- [ ] **Step 5: Run race detector**

Run: `go test -race ./internal/transport/...`
Expected: PASS — no race detected.

- [ ] **Step 6: Verify full build still works**

Run: `go build ./...`
Expected: success, no errors.

- [ ] **Step 7: Commit**

```bash
git add internal/transport/
git commit -m "feat(transport): Phase 1 — Transport interface와 Registry 추가"
```

---

### Task 2: Libp2pTransport adapter

**Files:**
- Create: `internal/transport/libp2p.go`
- Create: `internal/transport/libp2p_test.go`

- [ ] **Step 1: Write the failing integration test**

Create `internal/transport/libp2p_test.go`:

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/transport/...`
Expected: compile error — `NewLibp2pTransport` undefined.

- [ ] **Step 3: Implement Libp2pTransport**

Create `internal/transport/libp2p.go`:

```go
package transport

import (
	"context"
	"fmt"
	"io"

	bnet "github.com/hsleedevelop/bdpeer/internal/net"
	"github.com/hsleedevelop/bdpeer/internal/proto"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
)

// Libp2pTransport wraps a *bnet.Host so that the libp2p stream protocol
// /bdpeer/1.0.0 can be used through the Transport interface.
//
// PeerID values used with this transport must be valid libp2p peer.ID
// strings (i.e. peer.ID.String() output).
type Libp2pTransport struct {
	host    *bnet.Host
	handler Handler
}

// NewLibp2pTransport returns a transport that uses host's libp2p endpoint.
func NewLibp2pTransport(host *bnet.Host) *Libp2pTransport {
	return &Libp2pTransport{host: host}
}

// Name implements Transport.
func (t *Libp2pTransport) Name() string { return "libp2p" }

// OpenStream implements Transport. Opens a new libp2p stream on the
// /bdpeer/1.0.0 protocol. The caller writes the frame payload and Close()s.
func (t *Libp2pTransport) OpenStream(ctx context.Context, p PeerID) (io.ReadWriteCloser, error) {
	pid, err := peer.Decode(string(p))
	if err != nil {
		return nil, fmt.Errorf("transport/libp2p: invalid peer id %q: %w", p, err)
	}
	s, err := t.host.Libp2p.NewStream(ctx, pid, proto.Protocol)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNoConnection, err)
	}
	return s, nil
}

// SetHandler implements Transport. Registers the protocol stream handler.
// Replaces any previously registered handler.
func (t *Libp2pTransport) SetHandler(h Handler) {
	t.handler = h
	t.host.Libp2p.SetStreamHandler(proto.Protocol, func(s network.Stream) {
		from := PeerID(s.Conn().RemotePeer().String())
		// Run the handler in this goroutine — libp2p already spawned one for us.
		defer func() {
			if r := recover(); r != nil {
				// Don't let a handler panic kill the transport.
				_ = r
			}
		}()
		t.handler(from, s)
	})
}

// Close implements Transport. For libp2p this is a no-op — connection
// lifecycle is managed by the libp2p host itself.
func (t *Libp2pTransport) Close(_ PeerID) error { return nil }
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/transport/...`
Expected: PASS — 6 tests total (4 registry + 2 libp2p).

Note: the second test (`OpenStreamUnknownPeer`) constructs a real libp2p host; first run may take several seconds because of DHT bootstrap. This is expected — the test does not need DHT to succeed, only the host construction.

- [ ] **Step 5: Run race detector**

Run: `go test -race ./internal/transport/...`
Expected: PASS.

- [ ] **Step 6: Full build**

Run: `go build ./...`
Expected: success.

- [ ] **Step 7: Commit**

```bash
git add internal/transport/libp2p.go internal/transport/libp2p_test.go
git commit -m "feat(transport): libp2p adapter 추가 + 통합 테스트"
```

---

### Task 3: WebRTCTransport adapter

**Files:**
- Create: `internal/transport/webrtc.go`
- Create: `internal/transport/webrtc_test.go`

- [ ] **Step 1: Write the failing integration test**

Create `internal/transport/webrtc_test.go`:

```go
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

	// Wait for both sides to reach Connected state.
	select {
	case <-connA.Connected():
	case <-time.After(10 * time.Second):
		connA.Close()
		connB.Close()
		t.Fatalf("connA Connected timeout")
	}
	select {
	case <-connB.Connected():
	case <-time.After(10 * time.Second):
		connA.Close()
		connB.Close()
		t.Fatalf("connB Connected timeout")
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/transport/...`
Expected: compile error — `NewWebRTCTransport`, `(*WebRTCTransport).Attach` undefined.

- [ ] **Step 3: Implement WebRTCTransport**

Create `internal/transport/webrtc.go`:

```go
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
	mu      sync.RWMutex
	conns   map[PeerID]*webrtcEntry
	handler Handler
	handMu  sync.RWMutex
}

type webrtcEntry struct {
	conn    *bnet.WebRTCConn
	writeMu sync.Mutex
}

// NewWebRTCTransport returns an empty transport.
func NewWebRTCTransport() *WebRTCTransport {
	return &WebRTCTransport{conns: make(map[PeerID]*webrtcEntry)}
}

// Name implements Transport.
func (t *WebRTCTransport) Name() string { return "webrtc" }

// Attach registers an already-connected WebRTCConn under peer and spawns a
// goroutine that delivers inbound frames to the registered handler. Replaces
// any existing entry for peer (closing the old conn's reader path via
// SetHandler is the caller's responsibility — typical use is one Attach per
// peer for the lifetime of that connection).
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

// SetHandler implements Transport.
func (t *WebRTCTransport) SetHandler(h Handler) {
	t.handMu.Lock()
	t.handler = h
	t.handMu.Unlock()
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
// The handler is responsible for reading frames in a loop until EOF.
func (t *WebRTCTransport) runReader(peer PeerID, e *webrtcEntry) {
	t.handMu.RLock()
	h := t.handler
	t.handMu.RUnlock()
	if h == nil {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			_ = r // swallow — don't take down the transport
		}
	}()
	// The handler owns the conn for the duration of this inbound "stream".
	// It must not Close() the underlying *bnet.WebRTCConn — the transport
	// owns the connection's lifetime. We pass a noCloseRWC wrapper to make
	// that explicit.
	h(peer, &noCloseRWC{rwc: e.conn})
}

// webrtcStream is the per-call outbound wrapper. Writes go to the underlying
// DataChannel; Close releases the per-peer write mutex.
type webrtcStream struct {
	entry  *webrtcEntry
	closed bool
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
	if !s.closed {
		s.entry.writeMu.Unlock()
		s.closed = true
	}
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
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/transport/...`
Expected: PASS — 9 tests total (4 registry + 2 libp2p + 3 webrtc).

Note: WebRTC tests use loopback `PeerConnection`s. They depend on the local network stack allowing UDP loopback; on standard macOS/Linux dev machines this works. If the WebRTC pair fails to reach `Connected` within 10s on CI, mark the test `t.Skip("WebRTC loopback unsupported in this environment")` — do not weaken the timeout.

- [ ] **Step 5: Run race detector**

Run: `go test -race ./internal/transport/...`
Expected: PASS.

- [ ] **Step 6: Full build**

Run: `go build ./...`
Expected: success.

- [ ] **Step 7: Commit**

```bash
git add internal/transport/webrtc.go internal/transport/webrtc_test.go
git commit -m "feat(transport): WebRTC adapter 추가 + 루프백 통합 테스트"
```

---

### Task 4: Wire Registry and transports into Service (additive — both paths coexist)

**Files:**
- Modify: `internal/core/service.go`

This task only adds fields and constructs the transports. `Service.Send` is **not** changed yet — existing prefix-based logic continues to run. After this task the new types are reachable but unused.

- [ ] **Step 1: Add imports and fields to Service struct**

Open `internal/core/service.go`. Add the import (alongside existing imports at top of file):

```go
"github.com/hsleedevelop/bdpeer/internal/transport"
```

Modify the `Service` struct (currently at lines 57–66) to add three fields. Find:

```go
type Service struct {
	cfg         *config.Config
	cfgPath     string
	host        *bnet.Host
	recvDir     string
	stops       []func()
	events      chan Event
	mu          sync.Mutex
	webrtcConns map[string]*bnet.WebRTCConn // peerUUID → active WebRTC conn
}
```

Replace with:

```go
type Service struct {
	cfg         *config.Config
	cfgPath     string
	host        *bnet.Host
	recvDir     string
	stops       []func()
	events      chan Event
	mu          sync.Mutex
	webrtcConns map[string]*bnet.WebRTCConn // peerUUID → active WebRTC conn (legacy, removed in Task 9)
	registry    *transport.Registry
	libp2pT     *transport.Libp2pTransport
	webrtcT     *transport.WebRTCTransport
}
```

- [ ] **Step 2: Initialize registry in NewService**

Find `NewService` (currently lines 68–75):

```go
func NewService(cfg *config.Config, cfgPath string) *Service {
	return &Service{
		cfg:         cfg,
		cfgPath:     cfgPath,
		events:      make(chan Event, 256),
		webrtcConns: make(map[string]*bnet.WebRTCConn),
	}
}
```

Replace with:

```go
func NewService(cfg *config.Config, cfgPath string) *Service {
	return &Service{
		cfg:         cfg,
		cfgPath:     cfgPath,
		events:      make(chan Event, 256),
		webrtcConns: make(map[string]*bnet.WebRTCConn),
		registry:    transport.NewRegistry(),
		webrtcT:     transport.NewWebRTCTransport(),
	}
}
```

(`libp2pT` cannot be created here — `s.host` doesn't exist yet. It is created in `Start` after the host is constructed.)

- [ ] **Step 3: Construct Libp2pTransport in Service.Start**

In `Service.Start` (around line 103 where `s.host` is assigned), immediately after:

```go
s.host, err = bnet.NewHostWithIdentity(ctx, s.cfg.Nickname, priv)
if err != nil {
	return fmt.Errorf("new host: %w", err)
}
```

Add:

```go
s.libp2pT = transport.NewLibp2pTransport(s.host)
```

- [ ] **Step 4: Build and run existing tests**

Run: `go build ./...`
Expected: success.

Run: `go test ./...`
Expected: PASS — existing tests unchanged, plus transport tests passing.

- [ ] **Step 5: Commit**

```bash
git add internal/core/service.go
git commit -m "feat(core): Service에 transport.Registry + adapter 필드 추가 (additive)"
```

---

### Task 5: Hook libp2p Notifee — Register/Unregister on connect/disconnect

**Files:**
- Modify: `internal/net/host.go` (add hook callback to libp2pNotifee)
- Modify: `internal/core/service.go` (wire callback to Registry)

The libp2p `Connected/Disconnected` callbacks already exist in `internal/net/host.go` as part of `libp2pNotifee`. They currently do nothing with the Registry because Registry doesn't exist there (and `internal/net` should not depend on `internal/transport` — that would invert the dependency). We add a callback hook on `Host` so `internal/core` can wire it.

- [ ] **Step 1: Add `OnPeerConnected` and `OnPeerDisconnected` hooks to Host**

In `internal/net/host.go`, find the `Host` struct (currently lines 33–43):

```go
type Host struct {
	Libp2p       host.Host
	Nickname     string
	OnFrame      func(peer.ID, proto.Frame)
	OnFileStream func(peer.ID, proto.Frame, io.Reader)
	OnConnected  func(peer.ID)
	OnLog        func(string)
	mu           sync.RWMutex
	nicknames    map[peer.ID]string
	dht          *dht.IpfsDHT
}
```

Add two new optional callback fields **after `OnConnected`**:

```go
type Host struct {
	Libp2p          host.Host
	Nickname        string
	OnFrame         func(peer.ID, proto.Frame)
	OnFileStream    func(peer.ID, proto.Frame, io.Reader)
	OnConnected     func(peer.ID)
	OnDisconnected  func(peer.ID)
	OnLog           func(string)
	mu              sync.RWMutex
	nicknames       map[peer.ID]string
	dht             *dht.IpfsDHT
}
```

- [ ] **Step 2: Invoke `OnDisconnected` from the libp2pNotifee**

Find `Disconnected` in `internal/net/host.go` (currently lines 197–204):

```go
func (n *libp2pNotifee) Disconnected(_ network.Network, conn network.Conn) {
	id := conn.RemotePeer()
	// Only log disconnect for peers we actually knew (had a nickname).
	if nick := n.host.NicknameFor(id); nick != "" {
		n.host.emitLog("연결 끊김: " + nick)
	}
	n.mgr.Forget(id.String())
}
```

Replace with:

```go
func (n *libp2pNotifee) Disconnected(_ network.Network, conn network.Conn) {
	id := conn.RemotePeer()
	// Only log disconnect for peers we actually knew (had a nickname).
	if nick := n.host.NicknameFor(id); nick != "" {
		n.host.emitLog("연결 끊김: " + nick)
	}
	n.mgr.Forget(id.String())
	if n.host.OnDisconnected != nil {
		go n.host.OnDisconnected(id)
	}
}
```

(`OnConnected` is already invoked at `Connected`, line 193–195.)

- [ ] **Step 3: Wire callbacks in Service.Start**

In `internal/core/service.go`, find the existing `s.host.OnConnected = ...` assignment (currently lines 123–127):

```go
s.host.OnConnected = func(id peer.ID) {
	connCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_ = s.host.SendFrame(connCtx, id, proto.Frame{Type: proto.FrameHello, From: s.cfg.Nickname})
}
```

Replace with:

```go
s.host.OnConnected = func(id peer.ID) {
	s.registry.Register(transport.PeerID(id.String()), s.libp2pT)
	connCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_ = s.host.SendFrame(connCtx, id, proto.Frame{Type: proto.FrameHello, From: s.cfg.Nickname})
}
s.host.OnDisconnected = func(id peer.ID) {
	s.registry.Unregister(transport.PeerID(id.String()))
}
```

- [ ] **Step 4: Build and run all tests**

Run: `go build ./...`
Expected: success.

Run: `go test ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/net/host.go internal/core/service.go
git commit -m "feat(core): libp2p Notifee → Registry Register/Unregister 연결"
```

---

### Task 6: Hook BLE→WebRTC upgrader — Register/Unregister WebRTC peers (darwin only)

**Files:**
- Modify: `internal/core/service_ble.go`
- Modify: `internal/net/webrtc.go` (add disconnection hook)

The current code in `service_ble.go:handleWebRTCConn` stores connections in `s.webrtcConns` and has a defer that deletes them on read loop exit. We need to mirror those events to the Registry.

Note: `service_ble.go` is `//go:build darwin`. This task's modifications are darwin-only. Verify with both `go build ./...` (excludes service_ble.go on non-darwin) and on darwin via `go build ./...` (includes it).

- [ ] **Step 1: Add Registry.Register and webrtcT.Attach calls in handleWebRTCConn**

In `internal/core/service_ble.go`, find `handleWebRTCConn`. After the existing `s.webrtcConns[peerUUID] = conn` block (currently lines 64–67):

```go
// Register conn so Send() can reach this peer.
s.mu.Lock()
s.webrtcConns[peerUUID] = conn
s.mu.Unlock()
```

Replace with:

```go
// Register conn so Send() can reach this peer (legacy map, removed in Task 9).
s.mu.Lock()
s.webrtcConns[peerUUID] = conn
s.mu.Unlock()

// Also register in the new transport Registry.
peerKey := transport.PeerID("ble-" + peerUUID)
s.webrtcT.Attach(peerKey, conn)
s.registry.Register(peerKey, s.webrtcT)
```

Then find the existing read-loop defer (currently lines 76–81):

```go
defer func() {
	conn.Close()
	s.mu.Lock()
	delete(s.webrtcConns, peerUUID)
	s.mu.Unlock()
}()
```

Replace with:

```go
defer func() {
	conn.Close()
	s.mu.Lock()
	delete(s.webrtcConns, peerUUID)
	s.mu.Unlock()
	s.registry.Unregister(peerKey)
	// webrtcT.Close is a no-op vs the conn we already closed, but it
	// removes the per-peer entry so future OpenStream returns ErrNoConnection.
	_ = s.webrtcT.Close(peerKey)
}()
```

Add the import at the top of `internal/core/service_ble.go`:

```go
"github.com/hsleedevelop/bdpeer/internal/transport"
```

- [ ] **Step 2: Build for darwin and run tests**

Run: `go build ./...`
Expected: success on darwin (BLE is in default build per v0.3.1).

Run: `go test ./...`
Expected: PASS.

- [ ] **Step 3: Build for non-darwin (cross-compile sanity check)**

Run: `GOOS=linux go build ./...`
Expected: success — `service_ble.go` is excluded by build tag; `service_ble_stub.go` provides the no-op.

- [ ] **Step 4: Commit**

```bash
git add internal/core/service_ble.go
git commit -m "feat(core): BLE→WebRTC 연결을 Registry + WebRTCTransport에 등록"
```

---

### Task 7: Cut over Service.Send — replace prefix branch with Registry lookup

**Files:**
- Modify: `internal/core/service.go`

This is the cutover. After this task, `Send()` no longer parses `"ble-"` prefix. Routing is purely via Registry.

- [ ] **Step 1: Replace Service.Send body**

In `internal/core/service.go`, find `Service.Send` (currently lines 255–286):

```go
func (s *Service) Send(ctx context.Context, req SendRequest) error {
	// Check if target is a BLE→WebRTC peer (fake ID prefix "ble-").
	peerIDStr := string(req.To)
	if len(peerIDStr) > 4 && peerIDStr[:4] == "ble-" {
		peerUUID := peerIDStr[4:]
		s.mu.Lock()
		conn, ok := s.webrtcConns[peerUUID]
		s.mu.Unlock()
		if !ok {
			return fmt.Errorf("WebRTC 연결 없음: %s", peerUUID)
		}
		return s.sendViaWebRTC(ctx, conn, req)
	}

	if req.File != "" {
		pr, pw := io.Pipe()
		go func() {
			err := transfer.WriteFile(req.File, s.cfg.Nickname, pw)
			pw.CloseWithError(err)
		}()
		stream, err := s.host.Libp2p.NewStream(ctx, req.To, proto.Protocol)
		if err != nil {
			return err
		}
		defer stream.Close()
		_, err = io.Copy(stream, pr)
		return err
	}

	frame := proto.Frame{Type: proto.FrameText, From: s.cfg.Nickname, Content: req.Content}
	return s.host.SendFrame(ctx, req.To, frame)
}
```

Replace with:

```go
func (s *Service) Send(ctx context.Context, req SendRequest) error {
	peerKey := transport.PeerID(string(req.To))
	t, ok := s.registry.Lookup(peerKey)
	if !ok {
		return fmt.Errorf("unknown peer: %s", peerKey)
	}

	stream, err := t.OpenStream(ctx, peerKey)
	if err != nil {
		if errors.Is(err, transport.ErrNoConnection) {
			s.registry.Unregister(peerKey)
		}
		return err
	}
	defer stream.Close()

	if req.File != "" {
		pr, pw := io.Pipe()
		go func() {
			err := transfer.WriteFile(req.File, s.cfg.Nickname, pw)
			pw.CloseWithError(err)
		}()
		_, err = io.Copy(stream, pr)
		return err
	}

	return transfer.WriteFrame(stream, proto.Frame{
		Type: proto.FrameText, From: s.cfg.Nickname, Content: req.Content,
	})
}
```

- [ ] **Step 2: Remove sendViaWebRTC and unused imports**

Delete `Service.sendViaWebRTC` entirely (currently lines 288–306).

Add `"errors"` to the imports at the top of `internal/core/service.go` if not already present.

The imports `"bytes"` and `"io"` are still used elsewhere (`bytes` in `Service.Start` for file-stream handling; `io` for `io.Pipe`). Leave them.

The import `"github.com/hsleedevelop/bdpeer/internal/proto"` is still used (`proto.Frame`, `proto.Protocol` etc. in Start). Leave it.

The import `"github.com/libp2p/go-libp2p/core/peer"` is still used (`SendRequest.To peer.ID`, `Service.Start` peer.ID usages). Leave it.

- [ ] **Step 3: Build and verify type usage**

Run: `go build ./...`
Expected: success.

If the build complains about unused imports, remove them.

- [ ] **Step 4: Run all tests**

Run: `go test ./...`
Expected: PASS.

- [ ] **Step 5: Smoke test — manual (darwin)**

Run `bdpeer` from the build:

```bash
go build -o /tmp/bdpeer-task7 ./cmd/bdpeer
```

Launch two instances on the same Mac in two terminals (different nicknames via `~/Library/Application Support/bdpeer/config.json` or env). Send a text message from one to the other via the TUI. Verify it arrives.

If you do not have a second device handy, skip this step but document the skip in the commit message.

- [ ] **Step 6: Commit**

```bash
git add internal/core/service.go
git commit -m "refactor(core): Send를 Registry 경로로 cutover, ble- prefix 분기 제거"
```

---

### Task 8: Cut over inbound — unified handler + libp2p SetHandler

**Files:**
- Modify: `internal/core/service.go`

The libp2p inbound stream handler currently lives in `internal/net/stream.go` and is invoked via `s.host.StartStreamHandler()` (called from `Service.Start`). It builds frames and calls back to `s.host.OnFrame` / `s.host.OnFileStream`. We unify this into one `Service.onInboundStream` method registered on the libp2p Transport. The WebRTC side already routes through the same handler via `WebRTCTransport.runReader` once we register it in Task 9 — but the function we register must already exist, hence we add it now.

- [ ] **Step 1: Add Service.onInboundStream method**

In `internal/core/service.go`, add a new method (anywhere after `Service.Stop`):

```go
// onInboundStream is the unified inbound handler for all transports.
// It reads frames from stream until EOF and dispatches each frame to the
// Service event channel. Currently the from PeerID is informational; frame
// dispatching keys off frame.From (sender's nickname) and frame.Type.
func (s *Service) onInboundStream(from transport.PeerID, stream io.ReadWriteCloser) {
	defer stream.Close()
	r := bufio.NewReader(stream)
	for {
		frame, err := transfer.ReadFrame(r)
		if err != nil {
			return
		}
		switch frame.Type {
		case proto.FrameText:
			s.events <- Event{Type: EventTextReceived, From: frame.From, Content: frame.Content}

		case proto.FrameHello:
			if frame.From == "" {
				continue
			}
			// For libp2p peers we have a real peer.ID; for BLE→WebRTC the from
			// PeerID is "ble-<uuid>". RememberNickname is libp2p-specific so
			// only call it for libp2p-shaped IDs.
			if pid, err := peer.Decode(string(from)); err == nil {
				s.host.RememberNickname(pid, frame.From)
				addrs := s.host.Libp2p.Peerstore().Addrs(pid)
				s.log("닉네임 수신: " + frame.From + " (" + pid.String()[:8] + "...)")
				s.events <- Event{Type: EventPeerFound, Peer: bnet.PeerInfo{
					ID: pid, Nickname: frame.From, Addrs: addrs, Source: "dht",
				}}
			}

		case proto.FrameFileStart:
			s.events <- Event{Type: EventFileStart, From: frame.From, Name: frame.Name, Size: frame.Size}
			var startBuf bytes.Buffer
			_ = transfer.WriteFrame(&startBuf, frame)
			combined := io.MultiReader(&startBuf, r)
			savePath, err := transfer.ReadFile(combined, s.recvDir, func(recv, total int64) {
				s.events <- Event{Type: EventFileProgress, From: frame.From, Received: recv, Total: total}
			})
			if err != nil {
				s.events <- Event{Type: EventError, Err: err}
				return
			}
			s.events <- Event{Type: EventFileDone, From: frame.From, Name: frame.Name, Path: savePath}
			return // file stream consumed the rest of this logical stream
		}
	}
}
```

Add `"bufio"` to the imports at the top of `internal/core/service.go`.

- [ ] **Step 2: Replace s.host.StartStreamHandler() + OnFrame/OnFileStream wiring with libp2pT.SetHandler**

In `Service.Start`, find the existing block (currently lines 129–162):

```go
s.host.OnFrame = func(id peer.ID, frame proto.Frame) {
	switch frame.Type {
	case proto.FrameText:
		s.events <- Event{Type: EventTextReceived, From: frame.From, Content: frame.Content}
	case proto.FrameHello:
		if frame.From == "" {
			return
		}
		s.host.RememberNickname(id, frame.From)
		addrs := s.host.Libp2p.Peerstore().Addrs(id)
		s.log("닉네임 수신: " + frame.From + " (" + id.String()[:8] + "...)")
		s.events <- Event{Type: EventPeerFound, Peer: bnet.PeerInfo{
			ID: id, Nickname: frame.From, Addrs: addrs, Source: "dht",
		}}
	}
}
s.host.OnFileStream = func(_ peer.ID, startFrame proto.Frame, r io.Reader) {
	s.events <- Event{Type: EventFileStart, From: startFrame.From, Name: startFrame.Name, Size: startFrame.Size}

	var startBuf bytes.Buffer
	_ = transfer.WriteFrame(&startBuf, startFrame)
	combined := io.MultiReader(&startBuf, r)

	savePath, err := transfer.ReadFile(combined, s.recvDir, func(recv, total int64) {
		s.events <- Event{Type: EventFileProgress, From: startFrame.From, Received: recv, Total: total}
	})
	if err != nil {
		s.events <- Event{Type: EventError, Err: err}
		return
	}
	s.events <- Event{Type: EventFileDone, From: startFrame.From, Name: startFrame.Name, Path: savePath}
}

s.host.StartStreamHandler()
```

Replace with:

```go
s.libp2pT.SetHandler(s.onInboundStream)
```

`s.host.OnFrame`, `s.host.OnFileStream`, and `s.host.StartStreamHandler()` are no longer called. Leave the fields on `*Host` for now (Task 9 cleanup removes them if confirmed unused after WebRTC cutover).

- [ ] **Step 3: Build and verify**

Run: `go build ./...`
Expected: success.

Run: `go test ./...`
Expected: PASS.

- [ ] **Step 4: Smoke test — libp2p text message round trip**

Same as Task 7 Step 5 but verify that the inbound side still receives messages. Skip-with-note if no second device.

- [ ] **Step 5: Commit**

```bash
git add internal/core/service.go
git commit -m "refactor(core): libp2p inbound을 unified onInboundStream + libp2pT.SetHandler로 cutover"
```

---

### Task 9: Cut over WebRTC inbound + remove dead code (darwin)

**Files:**
- Modify: `internal/core/service_ble.go`
- Modify: `internal/core/service.go` (remove `webrtcConns` field, `mu` if unused)
- Modify: `internal/net/stream.go` (remove `StartStreamHandler` and `SendFrame` if unused)
- Modify: `internal/net/host.go` (remove `OnFrame`, `OnFileStream` if unused)

This task ends Phase 1.

- [ ] **Step 1: Register WebRTCTransport handler in Service.Start (alongside libp2p)**

In `internal/core/service.go`, after `s.libp2pT.SetHandler(s.onInboundStream)` added in Task 8, append on the next line:

```go
s.webrtcT.SetHandler(s.onInboundStream)
```

- [ ] **Step 2: Replace the per-peer read loop in handleWebRTCConn with reliance on WebRTCTransport's reader**

In `internal/core/service_ble.go`, the current code has:
1. A defer that deletes from `s.webrtcConns` and unregisters from Registry (kept from Task 6).
2. A `for { frame, err := transfer.ReadFrame(conn); ... }` loop that dispatches frames inline.

The transport now owns the reader (`WebRTCTransport.runReader` invokes `s.onInboundStream` once with the conn). So the inline loop in `handleWebRTCConn` is duplicate — it would race with the transport's reader.

Replace the entire goroutine block (currently lines 75–122) — the `go func() { defer ...; for { ReadFrame } }()` block — with **nothing**: the WebRTCTransport.runReader spawned by `Attach` (Task 6) already handles inbound frames via `s.onInboundStream`. The Hello frame write at line 70–72 still happens; that is outbound and stays.

The cleanup defer (closing conn, deleting `s.webrtcConns`, unregistering Registry, closing webrtcT) was inside the goroutine. We still need cleanup when the WebRTC conn dies. Move it to a state-change callback on the WebRTCConn itself.

After Task 9 Step 2's replacement, `handleWebRTCConn` looks like:

```go
func (s *Service) handleWebRTCConn(_ context.Context, peerUUID, peerNickname string, conn *bnet.WebRTCConn, mgr *discovery.Manager) {
	peerID := peer.ID("ble-" + peerUUID)
	if peerNickname != "" {
		mgr.Notify(discovery.DiscoveredPeer{
			ID:       peerID,
			Nickname: peerNickname,
			Addr:     peerUUID,
			Source:   "ble→webrtc",
		})
	}
	s.log("[BLE▸WTC] 연결 완료: " + peerNickname)

	peerKey := transport.PeerID("ble-" + peerUUID)
	s.webrtcT.Attach(peerKey, conn)
	s.registry.Register(peerKey, s.webrtcT)

	// Send Hello frame to the remote peer using the new transport.
	go func() {
		stream, err := s.webrtcT.OpenStream(context.Background(), peerKey)
		if err != nil {
			return
		}
		defer stream.Close()
		_ = transfer.WriteFrame(stream, proto.Frame{Type: proto.FrameHello, From: s.cfg.Nickname})
	}()
}
```

Drop the legacy `s.webrtcConns` map writes — the field is removed in Step 3.

Cleanup on disconnect: the `WebRTCConn` already triggers `pw.CloseWithError(io.ErrClosedPipe)` on `Failed/Disconnected/Closed` (see `internal/net/webrtc.go:52-62`). That closes the pipe, which causes `transfer.ReadFrame` in `onInboundStream` to return an error, which causes `onInboundStream` to return, which closes the `noCloseRWC` (no-op). At that point the transport's `runReader` returns. We still need to remove the peer from the Registry. Add it as a `PeerConnectionStateChange` hook by modifying the upgrader-set callback.

Hmm — `OnConnectionStateChange` is already wired inside `newWebRTCConn` (`internal/net/webrtc.go:52`). To get a notification at the Service layer, the simplest path is to add a `OnClose` callback on `WebRTCConn` that the Service sets after `Attach`. Add to `internal/net/webrtc.go`:

Find the `WebRTCConn` struct (currently lines 17–24):

```go
type WebRTCConn struct {
	pc          *webrtc.PeerConnection
	dc          *webrtc.DataChannel
	pr          *io.PipeReader
	pw          *io.PipeWriter
	once        sync.Once
	connectedCh chan struct{}
}
```

Add a field:

```go
type WebRTCConn struct {
	pc          *webrtc.PeerConnection
	dc          *webrtc.DataChannel
	pr          *io.PipeReader
	pw          *io.PipeWriter
	once        sync.Once
	connectedCh chan struct{}
	OnClose     func()
}
```

In `newWebRTCConn`, modify the state callback (currently lines 52–61):

```go
pc.OnConnectionStateChange(func(s webrtc.PeerConnectionState) {
	switch s {
	case webrtc.PeerConnectionStateConnected:
		c.once.Do(func() { close(c.connectedCh) })
	case webrtc.PeerConnectionStateFailed,
		webrtc.PeerConnectionStateDisconnected,
		webrtc.PeerConnectionStateClosed:
		pw.CloseWithError(io.ErrClosedPipe)
	}
})
```

Replace with:

```go
pc.OnConnectionStateChange(func(s webrtc.PeerConnectionState) {
	switch s {
	case webrtc.PeerConnectionStateConnected:
		c.once.Do(func() { close(c.connectedCh) })
	case webrtc.PeerConnectionStateFailed,
		webrtc.PeerConnectionStateDisconnected,
		webrtc.PeerConnectionStateClosed:
		pw.CloseWithError(io.ErrClosedPipe)
		if c.OnClose != nil {
			go c.OnClose()
		}
	}
})
```

Then in `handleWebRTCConn` (`internal/core/service_ble.go`), after `s.registry.Register(peerKey, s.webrtcT)`, add:

```go
conn.OnClose = func() {
	s.registry.Unregister(peerKey)
	_ = s.webrtcT.Close(peerKey)
}
```

- [ ] **Step 3: Remove legacy `webrtcConns` field, `mu` from Service**

In `internal/core/service.go`, the `webrtcConns` map and the `sync.Mutex mu` were used only for the legacy WebRTC routing path. Now that Task 7 removed `sendViaWebRTC` and Task 9 Step 2 removed the per-peer read loop, both are unused.

Find the `Service` struct and remove the `webrtcConns` and `mu` fields:

```go
type Service struct {
	cfg      *config.Config
	cfgPath  string
	host     *bnet.Host
	recvDir  string
	stops    []func()
	events   chan Event
	registry *transport.Registry
	libp2pT  *transport.Libp2pTransport
	webrtcT  *transport.WebRTCTransport
}
```

Find `NewService` and remove the `webrtcConns` initialization:

```go
func NewService(cfg *config.Config, cfgPath string) *Service {
	return &Service{
		cfg:      cfg,
		cfgPath:  cfgPath,
		events:   make(chan Event, 256),
		registry: transport.NewRegistry(),
		webrtcT:  transport.NewWebRTCTransport(),
	}
}
```

Remove the `"sync"` import from `internal/core/service.go` if no other code in that file references `sync`.

In `internal/core/service_ble.go`, remove the legacy map mutations: any remaining references to `s.webrtcConns[...]` or `s.mu.Lock`/`Unlock` related to that map. After Step 2 above there should be none — verify with `grep -n webrtcConns internal/core/service_ble.go` (should return nothing).

- [ ] **Step 4: Remove `internal/net/stream.go` `StartStreamHandler` and `SendFrame` if unused**

Search for callers:

```bash
grep -rn "StartStreamHandler\|\.SendFrame(" --include='*.go' .
```

`SendFrame` is still called by the `OnConnected` callback in `service.go` (sends Hello). That callback is still active. Decide:

Option A — leave `Host.SendFrame` and `Host.StartStreamHandler` for now since `SendFrame` has an in-tree caller.

Option B — also migrate the Hello-on-connect to use `libp2pT.OpenStream` + `transfer.WriteFrame`, then remove both functions.

Choose Option B for full cleanup. In `internal/core/service.go`, find the `OnConnected` assignment (after Task 5 modifications):

```go
s.host.OnConnected = func(id peer.ID) {
	s.registry.Register(transport.PeerID(id.String()), s.libp2pT)
	connCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_ = s.host.SendFrame(connCtx, id, proto.Frame{Type: proto.FrameHello, From: s.cfg.Nickname})
}
```

Replace with:

```go
s.host.OnConnected = func(id peer.ID) {
	peerKey := transport.PeerID(id.String())
	s.registry.Register(peerKey, s.libp2pT)
	connCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	stream, err := s.libp2pT.OpenStream(connCtx, peerKey)
	if err != nil {
		return
	}
	defer stream.Close()
	_ = transfer.WriteFrame(stream, proto.Frame{Type: proto.FrameHello, From: s.cfg.Nickname})
}
```

Now `Host.SendFrame` has no callers in the tree. Verify:

```bash
grep -rn "\.SendFrame(" --include='*.go' .
```

Should return nothing.

Delete `internal/net/stream.go` entirely:

```bash
git rm internal/net/stream.go
```

Remove `Host.OnFrame` and `Host.OnFileStream` fields from `internal/net/host.go` (they were only consumed by the deleted stream handler). Find the struct (after Task 5 modifications):

```go
type Host struct {
	Libp2p          host.Host
	Nickname        string
	OnFrame         func(peer.ID, proto.Frame)
	OnFileStream    func(peer.ID, proto.Frame, io.Reader)
	OnConnected     func(peer.ID)
	OnDisconnected  func(peer.ID)
	OnLog           func(string)
	mu              sync.RWMutex
	nicknames       map[peer.ID]string
	dht             *dht.IpfsDHT
}
```

Replace with:

```go
type Host struct {
	Libp2p         host.Host
	Nickname       string
	OnConnected    func(peer.ID)
	OnDisconnected func(peer.ID)
	OnLog          func(string)
	mu             sync.RWMutex
	nicknames      map[peer.ID]string
	dht            *dht.IpfsDHT
}
```

Remove the now-unused `"io"` and `"github.com/hsleedevelop/bdpeer/internal/proto"` imports from `internal/net/host.go` if there are no remaining references in that file (`grep -n 'proto\.' internal/net/host.go`).

- [ ] **Step 5: Build for all platforms**

```bash
go build ./...
GOOS=linux  go build ./...
GOOS=windows go build ./...
```

All three should succeed.

- [ ] **Step 6: Run all tests**

Run: `go test ./...`
Expected: PASS.

Run: `go test -race ./internal/transport/... ./internal/core/...`
Expected: PASS.

- [ ] **Step 7: Smoke test — libp2p + BLE→WebRTC end-to-end**

If two darwin devices are available, build with the BLE default and run on both. Verify:
- libp2p text exchange works.
- BLE-discovered peer appears in the sidebar.
- BLE→WebRTC text exchange works (sending in both directions).

If only one device is available, run two instances; libp2p path can be verified. Skip-with-note for the BLE leg.

- [ ] **Step 8: Commit**

```bash
git add -A
git commit -m "refactor(core,net): WebRTC inbound을 transport reader로 통합, 레거시 webrtcConns/stream.go 제거"
```

---

### Task 10: Documentation update

**Files:**
- Modify: `docs/superpowers/specs/2026-05-15-transport-abstraction-design.md` (add status note)
- Modify: `README.md` (optional — only if README references the old peerID branching publicly)

- [ ] **Step 1: Update spec status**

In `docs/superpowers/specs/2026-05-15-transport-abstraction-design.md`, change the first line under the title from:

```markdown
**Status:** Draft
```

to:

```markdown
**Status:** Implemented (commit <SHA of Task 9>)
```

Replace `<SHA of Task 9>` with the actual short SHA, obtained via `git rev-parse --short HEAD` after Task 9 commit.

- [ ] **Step 2: Check README for stale references**

Run: `grep -n 'ble-' README.md` and `grep -n 'webrtcConns' README.md`.
- If matches: replace or remove as appropriate to reflect that BLE→WebRTC routing is now transport-layered (no user-visible change).
- If no matches: no README change needed.

- [ ] **Step 3: Commit**

```bash
git add docs/ README.md
git commit -m "docs: Phase 1 transport abstraction 완료 표기"
```

---

## Self-Review Notes (for the executing engineer)

- After Task 9 the only place in the codebase that interprets `"ble-"` as a prefix is `internal/core/service_ble.go:handleWebRTCConn` and `discovery.DiscoveredPeer.ID` synthesis in the upgrader callback. Those are intentional — they are the *producers* of the synthetic ID, not consumers branching on it. The synthetic ID flows through Registry as an opaque key.
- `WebRTCTransport.runReader` is invoked once per `Attach` and exits when the underlying conn's pipe returns EOF. If a conn is re-attached for the same peerKey (shouldn't happen in Phase 1, but could in future fallback), the old reader will exit naturally when the old conn closes. No leak.
- `transport_test.go` does not construct libp2p hosts (Registry tests are pure unit). `libp2p_test.go` constructs hosts; first run is slow due to DHT bootstrap — acceptable.
- `webrtc_test.go` exchanges SDP via direct function calls (no BLE). If a future change moves SDP exchange into a non-public API, these tests must be updated. They are otherwise hermetic.
