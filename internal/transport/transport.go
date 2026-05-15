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

	// SetHandler registers the inbound stream callback. Must be called exactly
	// once per transport for its lifetime. Implementations may treat subsequent
	// calls as no-ops; behavior is not defined.
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
