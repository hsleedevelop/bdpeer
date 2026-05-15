# Transport Abstraction (Phase 1) — Design

**Status:** Draft
**Date:** 2026-05-15
**Scope:** Phase 1 of the "discovery path = data path" initiative. Introduce a Transport abstraction so future phases (BLE data channel, WebRTC↔BLE fallback) plug in cleanly. No external behavior changes.

## Goal

Replace the implicit `"ble-"` peerID prefix branching in `Service.Send` with an explicit `Registry → Transport` routing layer. Wrap the two existing connectivity backends (libp2p, BLE-signaled WebRTC) as `Transport` implementations behind a common interface.

## Non-Goals

- BLE-as-data-channel transport (Phase 2).
- WebRTC→BLE fallback logic (Phase 3).
- UI / log / sidebar changes — peerIDs continue to display as today, including the `"ble-"` prefix.
- Concurrent multi-stream per peer. Current call sites are serial; no virtual-stream multiplexing.
- New external APIs, new config keys, new wire formats.

## Architecture

A new `internal/transport/` package defines a `Transport` interface and a `Registry` that maps `PeerID → Transport`. Two adapters wrap existing code:

- `Libp2pTransport` wraps `*bnet.Host`.
- `WebRTCTransport` wraps the existing `webrtcConns` map of `*bnet.WebRTCConn`.

The discovery layer registers peers as their underlying connections are established (libp2p Notifee, BLE→WebRTC upgrader success). `Service.Send` becomes a Registry lookup followed by `Transport.OpenStream` + write + close. Inbound streams are delivered to a single Service-owned handler that each transport invokes.

Stream semantics follow the libp2p convention: **one logical send = one stream**. The WebRTC adapter serializes per-peer with a mutex because the underlying DataChannel is persistent and shared.

## Types

```go
package transport

import (
    "context"
    "errors"
    "io"
    "sync"
)

// PeerID is an opaque identifier. May be a libp2p peer.ID.String(),
// a "ble-<uuid>" string, or any future scheme. Routing is via Registry.
type PeerID string

// Handler is invoked by a Transport for each inbound logical stream.
// The transport calls Handler in its own goroutine. The handler owns
// the stream and is responsible for closing it.
type Handler func(from PeerID, stream io.ReadWriteCloser)

// Transport is one connectivity backend.
type Transport interface {
    // OpenStream opens a new outbound stream to peer. The caller writes
    // payload and Close()s. Returns ErrNoConnection if no underlying
    // connection is registered for peer.
    OpenStream(ctx context.Context, peer PeerID) (io.ReadWriteCloser, error)

    // SetHandler registers the inbound stream callback. Called once at startup.
    SetHandler(h Handler)

    // Close tears down state for a single peer. Does not stop the transport itself.
    Close(peer PeerID) error

    // Name returns "libp2p", "webrtc", etc. For logging.
    Name() string
}

var ErrNoConnection = errors.New("transport: no connection for peer")

// Registry routes peer → transport. Concurrent-safe.
type Registry struct {
    mu    sync.RWMutex
    peers map[PeerID]Transport
}

func NewRegistry() *Registry
func (r *Registry) Register(peer PeerID, t Transport)
func (r *Registry) Unregister(peer PeerID)
func (r *Registry) Lookup(peer PeerID) (Transport, bool)
```

`Register` on an existing key overwrites — this is intentional for future fallback (Phase 3 will replace `webrtcT` with `bleDataT` for a peer in place).

## Components

| File | Responsibility |
|---|---|
| `internal/transport/transport.go` | `PeerID`, `Transport` interface, `Handler`, `ErrNoConnection`, `Registry` |
| `internal/transport/libp2p.go` | `Libp2pTransport` wrapping `*bnet.Host` |
| `internal/transport/webrtc.go` | `WebRTCTransport` wrapping the per-peer `*bnet.WebRTCConn` map |
| `internal/transport/transport_test.go` | Registry unit tests |
| `internal/transport/libp2p_test.go` | Libp2p adapter integration test (in-process hosts) |
| `internal/transport/webrtc_test.go` | WebRTC adapter integration test (loopback PCs) |

Modifications:

| File | Change |
|---|---|
| `internal/core/service.go` | Add `registry *transport.Registry`. Replace `Send()` body with Registry lookup + `OpenStream` + write + close. Delete `sendViaWebRTC` and the `"ble-"` prefix branch. |
| `internal/core/service.go` (Notifee region) | On libp2p `Connected/Disconnected`, call `registry.Register/Unregister`. |
| `internal/core/service_ble.go` | On BLE→WebRTC upgrader success, call `registry.Register("ble-"+uuid, webrtcT)`. On WebRTC connection-state Failed/Closed, call `Unregister`. Replace `dc.OnMessage` direct handling with `webrtcT.SetHandler` flow. |
| `internal/net/stream.go` | Keep `SendFrame` and `StartStreamHandler` for now; `Libp2pTransport` calls into them. May be removed in step 8 if fully shadowed — decided during execution. |

The `internal/net` package is **not** restructured. The adapters in `internal/transport` call into existing `*bnet.Host` and `*bnet.WebRTCConn` methods.

## Data Flow

### Outbound

```
UI → Service.Send(peerID, req)
  ↓
t, ok := s.registry.Lookup(peerID)
if !ok { return fmt.Errorf("unknown peer: %s", peerID) }
  ↓
stream, err := t.OpenStream(ctx, peerID)
if errors.Is(err, transport.ErrNoConnection) {
    s.registry.Unregister(peerID)
    return err
}
  ↓
if req.File != "" {
    pr, pw := io.Pipe()
    go func() {
        err := transfer.WriteFile(req.File, s.cfg.Nickname, pw)
        pw.CloseWithError(err)
    }()
    _, err = io.Copy(stream, pr)
} else {
    err = transfer.WriteFrame(stream, proto.Frame{
        Type: proto.FrameText, From: s.cfg.Nickname, Content: req.Content,
    })
}
  ↓
stream.Close()
return err
```

### Inbound

Service registers a single handler at startup against each transport:

```go
s.libp2pT.SetHandler(s.onInboundStream)
s.webrtcT.SetHandler(s.onInboundStream)
```

`onInboundStream` is the unified version of today's libp2p stream handler (`internal/net/stream.go`) and the WebRTC `dc.OnMessage` consumer in `service_ble.go`. It reads frames via `transfer.ReadFrame` in a loop and dispatches them to the existing Service event channel that drives UI updates.

### Discovery → Registry binding

| Trigger | Call |
|---|---|
| libp2p `Notifee.Connected(c)` | `registry.Register(PeerID(c.RemotePeer().String()), libp2pT)` |
| libp2p `Notifee.Disconnected(c)` | `registry.Unregister(PeerID(c.RemotePeer().String()))` |
| BLE→WebRTC upgrader success (both offer and answer side) | `registry.Register(PeerID("ble-"+uuid), webrtcT)` |
| WebRTC `PeerConnectionStateFailed`, `Disconnected`, `Closed` | `registry.Unregister(PeerID("ble-"+uuid))` |

The libp2p hooks already exist in `service.go`; the BLE hooks already exist in `service_ble.go`. The change is replacing direct `s.webrtcConns[uuid] = conn` map writes with `Register` calls (the map itself moves into `WebRTCTransport`).

## Adapter Implementation Notes

### `Libp2pTransport`

```go
type Libp2pTransport struct {
    host    *bnet.Host
    handler transport.Handler
}

func (t *Libp2pTransport) OpenStream(ctx, peer) (io.ReadWriteCloser, error) {
    pid, err := decodeLibp2pPeerID(string(peer))
    if err != nil { return nil, err }
    s, err := t.host.Libp2p.NewStream(ctx, pid, proto.Protocol)
    if err != nil { return nil, transport.ErrNoConnection }  // or wrap
    return s, nil
}

func (t *Libp2pTransport) SetHandler(h transport.Handler) {
    t.handler = h
    t.host.Libp2p.SetStreamHandler(proto.Protocol, func(s network.Stream) {
        from := transport.PeerID(s.Conn().RemotePeer().String())
        t.handler(from, s)
    })
}
```

`Close(peer)` is a no-op for libp2p (host manages connections globally).

### `WebRTCTransport`

```go
type WebRTCTransport struct {
    mu      sync.RWMutex
    conns   map[transport.PeerID]*webrtcEntry
    handler transport.Handler
}

type webrtcEntry struct {
    conn     *bnet.WebRTCConn
    writeMu  sync.Mutex
    readOnce sync.Once
}

// Called by service_ble.go on upgrader success.
func (t *WebRTCTransport) Attach(peer transport.PeerID, conn *bnet.WebRTCConn) {
    t.mu.Lock()
    t.conns[peer] = &webrtcEntry{conn: conn}
    t.mu.Unlock()
    t.startReader(peer)
}
```

`OpenStream` returns a per-call wrapper:

```go
type webrtcStream struct {
    entry  *webrtcEntry
    closed bool
}

func (s *webrtcStream) Write(p []byte) (int, error) { return s.entry.conn.Write(p) }
func (s *webrtcStream) Read(p []byte)  (int, error) { return 0, io.EOF }  // outbound stream — reads unused
func (s *webrtcStream) Close() error {
    if !s.closed { s.entry.writeMu.Unlock(); s.closed = true }
    return nil
}

func (t *WebRTCTransport) OpenStream(ctx, peer) (io.ReadWriteCloser, error) {
    t.mu.RLock()
    e, ok := t.conns[peer]
    t.mu.RUnlock()
    if !ok { return nil, transport.ErrNoConnection }
    e.writeMu.Lock()  // released by Close()
    return &webrtcStream{entry: e}, nil
}
```

Inbound: `startReader(peer)` spins one goroutine per peer that reads `transfer.ReadFrame` from the persistent `WebRTCConn.Read` pipe. Each frame is wrapped in a `bytes.Buffer`-backed `io.ReadWriteCloser` and passed to `t.handler(peer, framedStream)`. On `io.EOF` from the pipe the goroutine exits and calls `t.Close(peer)`.

Rationale for handing the handler a per-frame stream instead of a per-connection stream: it matches the libp2p semantic (one inbound stream = one frame's worth of bytes) and lets the unified `onInboundStream` handler be written once without branching on transport.

## Error Handling

| Case | Behavior |
|---|---|
| `Registry.Lookup` miss | `Service.Send` returns `fmt.Errorf("unknown peer: %s", peerID)`. |
| `OpenStream` underlying connection dead | Transport returns `ErrNoConnection`. `Service.Send` calls `registry.Unregister(peer)` and returns the error to UI. |
| Inbound handler panics | Each transport wraps its handler invocation in `defer recover()` and logs. Transport does not die. |
| `transfer.ReadFrame` EOF | Inbound reader exits cleanly. Not an error. |
| libp2p stream reset | Error propagates from `transfer.ReadFrame` / `Write`. Notifee.Disconnected handles unregister separately. |
| WebRTC `PeerConnectionStateFailed` | `service_ble.go`'s existing state callback calls `Unregister`. Any in-flight `OpenStream` returns `ErrNoConnection` on next call. |

## Migration Plan

Each step compiles and `go test ./...` passes.

1. Create `internal/transport/` package — interface, `Registry`, `ErrNoConnection`. No consumers yet.
2. Implement and unit-test `Registry`.
3. Implement `Libp2pTransport` + integration test (two in-process libp2p hosts).
4. Implement `WebRTCTransport` + integration test (two loopback `*webrtc.PeerConnection`s, skipping BLE).
5. Add `*transport.Registry`, `*Libp2pTransport`, `*WebRTCTransport` fields to `Service`. Wire them at `Start`. **Leave existing `Send` code intact** — both paths coexist temporarily.
6. Add `Register`/`Unregister` calls in:
   - libp2p Notifee hooks (`service.go`)
   - BLE upgrader success / WebRTC state callbacks (`service_ble.go`)
   The map `s.webrtcConns` is now mirrored by `WebRTCTransport.conns`; keep both temporarily.
7. Cut over `Service.Send`: replace body with Registry lookup + `OpenStream` flow. Delete `sendViaWebRTC` and the `"ble-"` prefix branch. Manual smoke test (build + send between two libp2p peers; build with BLE and send between two BLE peers).
8. Cut over inbound:
   - Replace `host.StartStreamHandler()` call with `libp2pT.SetHandler(s.onInboundStream)`.
   - Replace per-connection `dc.OnMessage` setup in `service_ble.go` with attaching connections to `WebRTCTransport` (which spins its reader internally) plus `webrtcT.SetHandler(s.onInboundStream)`.
9. Remove dead code: `s.webrtcConns`, `sendViaWebRTC`, any now-unused helpers.

Each step is its own commit. Steps 1–6 are additive — safe to revert. Step 7 is the cutover. 8–9 are cleanup.

## Testing

| Layer | Tests |
|---|---|
| `Registry` | Register/Lookup/Unregister, overwrite-on-reregister, concurrent access (`-race`). |
| `Libp2pTransport` | Two in-process hosts, A.OpenStream(B) → write frame → close, verify B's handler received frame. Missing-peer → `ErrNoConnection`. |
| `WebRTCTransport` | Two loopback `*PeerConnection`s paired via direct SDP exchange (no BLE). Attach both, OpenStream → write → close, verify inbound handler. Concurrent `OpenStream` calls to the same peer serialize without deadlock. Missing-peer → `ErrNoConnection`. |
| Existing tests | `go test ./...` passes unchanged. No new e2e tests — Phase 1 is a refactor with no external behavior change. |

## Open Risks

- **`WebRTCConn.Read` is shared**: the persistent pipe is owned by `WebRTCTransport`'s reader goroutine. If any other code path still reads from `conn.Read`, it will race. Step 9 cleanup must verify nothing outside the transport reads the conn.
- **Stream handler timing in libp2p**: `SetStreamHandler` replaces any previously registered handler. Phase 1 must register the transport's handler before any inbound stream can arrive. Today the handler is set in `host.StartStreamHandler()` called from `Service.Start`; the equivalent must happen in step 8 cutover.
- **`Close(peer)` ambiguity**: libp2p adapter treats it as a no-op (host owns connections). WebRTC adapter tears down the per-peer entry and stops the reader. Document this asymmetry in code comments so future transports follow the WebRTC pattern.
