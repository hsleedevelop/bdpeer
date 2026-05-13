# bdpeer TUI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Cross-platform P2P TUI app that discovers peers via every available local/internet protocol (Bonjour, SSDP, WS-Discovery, BLE, DHT) and transfers text and files over a unified libp2p stream.

**Architecture:** Build `bdpeer-core` first in Go, then attach a thin TUI adapter. The core owns libp2p host lifecycle, identity persistence, discovery startup, text/file transfer, and event emission. A `discovery.Manager` aggregates peer events from all protocol backends (mDNS/Bonjour, SSDP, WSD, BLE) into a single `OnPeerFound` callback inside the core. Discovery backends must advertise a libp2p-reachable multiaddr including `/p2p/<peerID>` whenever possible; the UI and send path keep using one `net.PeerInfo` shape. libp2p handles actual transport (TCP + QUIC). DHT peer discovery can be added after local/direct transfer is stable; circuit relay requires explicit relay bootstrap/reservation work and is not complete in the initial host scaffold. Bubble Tea owns only presentation/input state and talks to core through channels/methods.

**Core boundary:** `internal/core` is the reusable `bdpeer-core` package. `cmd/bdpeer` should only load config, create `core.Service`, start Bubble Tea, translate core events into UI messages, and pass user actions back to `core.Service.Send`. Future TypeScript/Electron/Tauri UI can reuse the same core through a CLI, local RPC, or FFI wrapper without rewriting networking.

**Tech Stack:** Go 1.22+, github.com/charmbracelet/bubbletea, github.com/charmbracelet/lipgloss, github.com/charmbracelet/bubbles, github.com/libp2p/go-libp2p, github.com/grandcat/zeroconf (Bonjour), github.com/koron/go-ssdp (SSDP/UPnP), tinygo.org/x/bluetooth (BLE cross-platform)

## Protocol Feasibility Matrix

| Protocol | Platforms | What it enables | Library |
|---|---|---|---|
| **mDNS/Bonjour** `_bdpeer._tcp` | All (macOS, iOS, Android, Win10+, Linux) | Peer discovery on local WiFi; iOS/macOS devices see our service | `grandcat/zeroconf` |
| **SSDP/UPnP** | Windows, Android | Windows Network, some Android discovery apps | `koron/go-ssdp` |
| **WS-Discovery** | Windows | Appears in Windows Explorer → Network | custom UDP multicast |
| **BLE Advertisement** | macOS, Linux, Windows (build tags) | Proximity discovery without WiFi router | `tinygo-org/bluetooth` |
| **libp2p DHT** | All (internet) | Find peers globally by peer ID | `libp2p/go-libp2p-kad-dht` |
| **Direct IP** | All | Manual connect by IP:port | `libp2p` multiaddr |

**AirDrop note:** AirDrop uses Apple's proprietary AWDL (Apple Wireless Direct Link) — not standard WiFi Direct. Cannot be implemented without Apple private APIs. Our app IS discoverable on the same WiFi network via Bonjour (`_bdpeer._tcp`) from iOS/macOS, but won't appear in the AirDrop sheet itself.

**Quick Share note:** Google's Nearby Connections protocol (BLE + WiFi Direct + mDNS). Our app appears in mDNS browsers on Android but not in the Quick Share UI. Full interop requires implementing the Nearby Connections wire protocol (Phase 3).

**Package naming note:** This plan keeps `internal/net` for now and aliases it as `bnet` from callers. If the implementation feels noisy, rename it to `internal/p2p` before code grows; do not rename midway through feature work.

**Frame encoding note:** File chunks are carried as JSON `[]byte`, which means base64 encoding and roughly 33% payload overhead. Accept this for the first working core because it keeps the frame codec simple and testable. If large-file throughput becomes a priority, replace only the frame codec with length-prefixed binary payloads while keeping the same `proto.Frame` semantics.

---

## Implementation Task Breakdown

실행 순서 체크리스트는 별도 파일로 분리했다 → [2026-05-13-bdpeer-tui-tasks.md](2026-05-13-bdpeer-tui-tasks.md).

해당 파일은 Phase 1(bdpeer-core) → Phase 2(TUI adapter) → Phase 3(packaging) 순서로 체크박스를 가지며, 각 항목은 본 plan의 §Task N으로 역참조된다. 새 작업을 시작하기 전에 task 파일의 다음 미체크 항목과 plan의 대응 섹션을 함께 열어두면 된다.

---

## File Map

```
bdpeer/
├── cmd/bdpeer/main.go              # Entry: parse flags, load config, start TUI
├── go.mod
├── Makefile                        # build targets per platform + BLE/noBLE tags
├── internal/
│   ├── config/
│   │   └── config.go              # Nickname + data dir (JSON, XDG-based path)
│   ├── core/
│   │   └── service.go             # bdpeer-core facade: Start, Stop, Events, Send
│   ├── discovery/
│   │   ├── manager.go             # Aggregates all backends → single OnPeerFound
│   │   ├── bonjour.go             # mDNS/Bonjour via zeroconf (_bdpeer._tcp)
│   │   ├── ssdp.go                # SSDP/UPnP for Windows/Android discovery
│   │   ├── wsd.go                 # WS-Discovery for Windows Network folder
│   │   ├── ble_stub.go            # //go:build !ble — no-op when BLE not available
│   │   ├── ble_linux.go           # //go:build ble,linux — BlueZ via tinygo bluetooth
│   │   ├── ble_darwin.go          # //go:build ble,darwin — CoreBluetooth via tinygo bluetooth
│   │   └── ble_windows.go         # //go:build ble,windows — WinRT via tinygo bluetooth
│   ├── proto/
│   │   └── protocol.go           # /bdpeer/1.0.0 frame types, ChunkSize
│   ├── net/
│   │   ├── host.go               # libp2p host init (TCP + QUIC, DHT)
│   │   ├── stream.go             # stream handler + SendFrame
│   │   └── connect.go            # ConnectByAddr, FirstTCPAddr
│   ├── transfer/
│   │   ├── message.go            # Encode/decode TEXT frames
│   │   └── file.go              # Chunked FILE_START/CHUNK/END frames
│   └── ui/
│       ├── app.go               # Bubble Tea root Model + Update + View
│       ├── setup.go             # Nickname input screen (first run)
│       ├── peerlist.go          # Left panel: discovered peers + "add IP"
│       ├── chat.go              # Right panel: messages + input
│       ├── progress.go          # File transfer progress bar overlay
│       └── styles.go            # Lipgloss color scheme (Nord palette)
```

### Interface contracts (locked in Task 1, used everywhere)

```go
// internal/proto/protocol.go
const Protocol = "/bdpeer/1.0.0"

type FrameType string
const (
    FrameText      FrameType = "text"
    FrameFileStart FrameType = "file_start"
    FrameFileChunk FrameType = "file_chunk"
    FrameFileEnd   FrameType = "file_end"
)

type Frame struct {
    Type     FrameType `json:"type"`
    From     string    `json:"from"`
    Content  string    `json:"content,omitempty"`   // FrameText
    Name     string    `json:"name,omitempty"`       // FrameFileStart
    Size     int64     `json:"size,omitempty"`       // FrameFileStart
    Seq      int       `json:"seq,omitempty"`        // FrameFileChunk
    Data     []byte    `json:"data,omitempty"`       // FrameFileChunk (raw bytes)
    Checksum string    `json:"checksum,omitempty"`   // FrameFileEnd (sha256:hex)
}

// internal/net/host.go
type PeerInfo struct {
    ID       peer.ID
    Nickname string
    Addrs    []multiaddr.Multiaddr
    Source   string
}

// ui/app.go — Bubble Tea messages (dispatched from goroutines)
type MsgPeerFound    struct{ Info net.PeerInfo }
type MsgPeerLost     struct{ ID peer.ID }
type MsgTextReceived struct{ From, Content string }
type MsgFileStart    struct{ From, Name string; Size int64 }
type MsgFileProgress struct{ From string; Received int64; Total int64 }
type MsgFileDone     struct{ From, Name, Path string }
type MsgError        struct{ Err error }

// internal/core/service.go — reusable bdpeer-core API
type EventType string
const (
    EventPeerFound    EventType = "peer_found"
    EventPeerLost     EventType = "peer_lost"
    EventTextReceived EventType = "text_received"
    EventFileStart    EventType = "file_start"
    EventFileProgress EventType = "file_progress"
    EventFileDone     EventType = "file_done"
    EventError        EventType = "error"
)

type Event struct {
    Type     EventType
    Peer     net.PeerInfo
    From     string
    Content  string
    Name     string
    Size     int64
    Received int64
    Total    int64
    Path     string
    Err      error
}

type SendRequest struct {
    To      peer.ID
    Content string
    File    string
}
```

**Import boundary rule:** `internal/proto` contains frame/protocol definitions and imports no project packages. `internal/transfer` imports `internal/proto`. `internal/net` imports both `internal/proto` and `internal/transfer`. `internal/transfer` must never import `internal/net`; otherwise Go rejects the cycle.

---

## Task 1: Project Scaffold

**Files:**
- Create: `go.mod`
- Create: `cmd/bdpeer/main.go`
- Create: `Makefile`
- Create: `internal/proto/protocol.go` (frame/protocol definitions only)
- Create: `internal/net/host.go` (initial `PeerInfo` type only)

- [ ] **Step 1: Initialize Go module**

```bash
cd /Users/chad/Projects/workspace/bdpeer
go mod init github.com/chad/bdpeer
```

Expected: `go.mod` created with `module github.com/chad/bdpeer` and `go 1.22`

- [ ] **Step 2: Add dependencies**

```bash
go get github.com/charmbracelet/bubbletea@latest
go get github.com/charmbracelet/lipgloss@latest
go get github.com/charmbracelet/bubbles@latest
go get github.com/libp2p/go-libp2p@latest
go get github.com/multiformats/go-multiaddr@latest
```

- [ ] **Step 3: Create protocol type definitions**

Create `internal/proto/protocol.go`:

```go
package proto

const Protocol = "/bdpeer/1.0.0"
const ChunkSize = 32 * 1024 // 32 KB

type FrameType string

const (
    FrameText      FrameType = "text"
    FrameFileStart FrameType = "file_start"
    FrameFileChunk FrameType = "file_chunk"
    FrameFileEnd   FrameType = "file_end"
)

type Frame struct {
    Type     FrameType `json:"type"`
    From     string    `json:"from"`
    Content  string    `json:"content,omitempty"`
    Name     string    `json:"name,omitempty"`
    Size     int64     `json:"size,omitempty"`
    Seq      int       `json:"seq,omitempty"`
    Data     []byte    `json:"data,omitempty"`
    Checksum string    `json:"checksum,omitempty"`
}
```

Create `internal/net/host.go` with the initial shared peer type:

```go
package net

import "github.com/libp2p/go-libp2p/core/peer"
import "github.com/multiformats/go-multiaddr"

type PeerInfo struct {
    ID       peer.ID
    Nickname string
    Addrs    []multiaddr.Multiaddr
    Source   string
}
```

- [ ] **Step 4: Create minimal entry point**

Create `cmd/bdpeer/main.go`:

```go
package main

import "fmt"

func main() {
    fmt.Println("bdpeer starting...")
}
```

- [ ] **Step 5: Create Makefile**

Create `Makefile`:

```makefile
.PHONY: build build-mac build-win build-linux

build:
	go build -o dist/bdpeer ./cmd/bdpeer

build-mac:
	GOOS=darwin GOARCH=amd64 go build -o dist/bdpeer-mac-amd64 ./cmd/bdpeer
	GOOS=darwin GOARCH=arm64 go build -o dist/bdpeer-mac-arm64 ./cmd/bdpeer

build-win:
	GOOS=windows GOARCH=amd64 go build -o dist/bdpeer-windows-amd64.exe ./cmd/bdpeer

build-linux:
	GOOS=linux GOARCH=amd64 go build -o dist/bdpeer-linux-amd64 ./cmd/bdpeer

test:
	go test ./...
```

- [ ] **Step 6: Verify build**

```bash
go build ./...
```

Expected: no errors

- [ ] **Step 7: Commit**

```bash
git init
git add go.mod go.sum cmd/ internal/ Makefile
git commit -m "feat: scaffold Go module and protocol type definitions"
```

---

## Task 2: Config (Nickname Persistence)

**Files:**
- Create: `internal/config/config.go`
- Create: `internal/config/config_test.go`

- [ ] **Step 1: Write failing test**

Create `internal/config/config_test.go`:

```go
package config_test

import (
    "os"
    "path/filepath"
    "testing"

    "github.com/chad/bdpeer/internal/config"
)

func TestSaveAndLoad(t *testing.T) {
    dir := t.TempDir()
    cfg := &config.Config{Nickname: "alice", DataDir: dir}

    if err := cfg.Save(filepath.Join(dir, "config.json")); err != nil {
        t.Fatalf("Save: %v", err)
    }

    loaded, err := config.Load(filepath.Join(dir, "config.json"))
    if err != nil {
        t.Fatalf("Load: %v", err)
    }
    if loaded.Nickname != "alice" {
        t.Errorf("got nickname %q, want %q", loaded.Nickname, "alice")
    }
    if loaded.DataDir != dir {
        t.Errorf("got data dir %q, want %q", loaded.DataDir, dir)
    }
}

func TestDefaultPath(t *testing.T) {
    path := config.DefaultPath()
    if path == "" {
        t.Error("DefaultPath should not be empty")
    }
    // must end in config.json
    if filepath.Base(path) != "config.json" {
        t.Errorf("got %q, want basename config.json", path)
    }
}

func TestLoadMissing(t *testing.T) {
    cfg, err := config.Load("/nonexistent/path/config.json")
    if err != nil {
        t.Fatalf("Load of missing file should return defaults, got: %v", err)
    }
    if cfg.Nickname != "" {
        t.Errorf("expected empty nickname for fresh config")
    }
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/config/...
```

Expected: FAIL — `config` package does not exist

- [ ] **Step 3: Implement config**

Create `internal/config/config.go`:

```go
package config

import (
    "encoding/json"
    "errors"
    "os"
    "path/filepath"
    "runtime"
)

type Config struct {
    Nickname      string `json:"nickname"`
    DataDir       string `json:"data_dir"`
    PrivateKeyB64 string `json:"private_key_b64,omitempty"` // libp2p identity; keep peer ID stable
}

func DefaultPath() string {
    switch runtime.GOOS {
    case "windows":
        base := os.Getenv("APPDATA")
        if base == "" {
            base, _ = os.UserHomeDir()
        }
        return filepath.Join(base, "bdpeer", "config.json")
    case "darwin":
        home, _ := os.UserHomeDir()
        return filepath.Join(home, "Library", "Application Support", "bdpeer", "config.json")
    default: // linux + others
        if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
            return filepath.Join(xdg, "bdpeer", "config.json")
        }
        home, _ := os.UserHomeDir()
        return filepath.Join(home, ".config", "bdpeer", "config.json")
    }
}

func Load(path string) (*Config, error) {
    f, err := os.Open(path)
    if errors.Is(err, os.ErrNotExist) {
        return &Config{}, nil
    }
    if err != nil {
        return nil, err
    }
    defer f.Close()

    var cfg Config
    if err := json.NewDecoder(f).Decode(&cfg); err != nil {
        return nil, err
    }
    return &cfg, nil
}

func (c *Config) Save(path string) error {
    if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
        return err
    }
    f, err := os.CreateTemp(filepath.Dir(path), ".bdpeer-cfg-*")
    if err != nil {
        return err
    }
    if err := json.NewEncoder(f).Encode(c); err != nil {
        f.Close()
        os.Remove(f.Name())
        return err
    }
    f.Close()
    return os.Rename(f.Name(), path)
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test ./internal/config/... -v
```

Expected: PASS (3 tests)

- [ ] **Step 5: Commit**

```bash
git add internal/config/
git commit -m "feat: add config persistence with XDG/platform paths"
```

---

## Task 3: libp2p Host

**Files:**
- Create: `internal/net/host.go`
- Create: `internal/net/host_test.go`

- [ ] **Step 1: Write failing test**

Create `internal/net/host_test.go`:

```go
package net_test

import (
    "context"
    "testing"
    "time"

    bnet "github.com/chad/bdpeer/internal/net"
)

func TestNewHost(t *testing.T) {
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()

    h, err := bnet.NewHost(ctx, "alice")
    if err != nil {
        t.Fatalf("NewHost: %v", err)
    }
    defer h.Close()

    if h.Libp2p.ID() == "" {
        t.Error("host ID should not be empty")
    }
    if len(h.Libp2p.Addrs()) == 0 {
        t.Error("host should have at least one listen addr")
    }
    if h.Nickname != "alice" {
        t.Errorf("got nickname %q, want %q", h.Nickname, "alice")
    }
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/net/... -run TestNewHost
```

Expected: FAIL — `NewHost` undefined

- [ ] **Step 3: Implement host**

Create `internal/net/host.go`:

```go
package net

import (
    "context"
    "fmt"
    "sync"

    libp2p "github.com/libp2p/go-libp2p"
    "github.com/libp2p/go-libp2p/core/crypto"
    "github.com/libp2p/go-libp2p/core/host"
    "github.com/libp2p/go-libp2p/core/peer"
    "github.com/multiformats/go-multiaddr"
    dht "github.com/libp2p/go-libp2p-kad-dht"
)

type PeerInfo struct {
    ID       peer.ID
    Nickname string
    Addrs    []multiaddr.Multiaddr
    Source   string
}

type Host struct {
    Libp2p   host.Host
    Nickname string
    mu       sync.RWMutex
    nicknames map[peer.ID]string
    dht      *dht.IpfsDHT
}

func NewHost(ctx context.Context, nickname string) (*Host, error) {
    priv, _, err := crypto.GenerateKeyPair(crypto.Ed25519, -1)
    if err != nil {
        return nil, fmt.Errorf("generate key: %w", err)
    }
    return NewHostWithIdentity(ctx, nickname, priv)
}

func NewHostWithIdentity(ctx context.Context, nickname string, priv crypto.PrivKey) (*Host, error) {
    h, err := libp2p.New(
        libp2p.Identity(priv),
        libp2p.ListenAddrStrings(
            "/ip4/0.0.0.0/tcp/0",
            "/ip4/0.0.0.0/udp/0/quic-v1",
        ),
        libp2p.NATPortMap(),
    )
    if err != nil {
        return nil, fmt.Errorf("new libp2p host: %w", err)
    }

    kd, err := dht.New(ctx, h, dht.Mode(dht.ModeAuto))
    if err != nil {
        h.Close()
        return nil, fmt.Errorf("new DHT: %w", err)
    }

    if err := kd.Bootstrap(ctx); err != nil {
        h.Close()
        return nil, fmt.Errorf("DHT bootstrap: %w", err)
    }

    // TODO(internet-p2p): add explicit bootstrap peers, e.g.
    // dht.BootstrapPeers(dht.GetDefaultBootstrapPeerAddrInfos()...)
    // and later circuit relay reservation/bootstrap configuration.

    return &Host{Libp2p: h, Nickname: nickname, nicknames: make(map[peer.ID]string), dht: kd}, nil
}

func (h *Host) RememberNickname(id peer.ID, nickname string) {
    if id == "" || nickname == "" {
        return
    }
    h.mu.Lock()
    h.nicknames[id] = nickname
    h.mu.Unlock()
}

func (h *Host) nicknameFor(id peer.ID) string {
    h.mu.RLock()
    nick := h.nicknames[id]
    h.mu.RUnlock()
    if nick != "" {
        return nick
    }
    if id != "" {
        s := id.String()
        if len(s) > 8 {
            return s[:8]
        }
        return s
    }
    return "unknown"
}

func (h *Host) Close() error {
    if h.dht != nil {
        h.dht.Close()
    }
    return h.Libp2p.Close()
}
```

> Follow-up tasks register a `discovery.Manager` after host creation with `host.AttachDiscovery(mgr)`. Keep `NewHost(ctx, nickname)` stable so tests, direct-IP connections, and TUI wiring use one constructor throughout the plan.
> Task 3b persists the libp2p private key in config (`PrivateKeyB64`) so peer ID stays stable across app launches.

- [ ] **Step 4: Add missing go-libp2p-kad-dht dependency**

```bash
go get github.com/libp2p/go-libp2p-kad-dht@latest
go mod tidy
```

- [ ] **Step 5: Run test to verify it passes**

```bash
go test ./internal/net/... -run TestNewHost -v -timeout 15s
```

Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/net/host.go internal/net/host_test.go go.mod go.sum
git commit -m "feat: initialize libp2p host with TCP, QUIC, and DHT"
```

---

## Task 3b: Persist libp2p Identity

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/net/host.go`
- Create: `internal/net/identity_test.go`

The app must not generate a new libp2p identity on every launch. Store the private key in config and reuse it so peer IDs remain stable.

- [ ] **Step 1: Add identity helpers**

Add to `internal/net/host.go`:

```go
import (
    "encoding/base64"

    "github.com/libp2p/go-libp2p/core/crypto"
)

func GenerateIdentityB64() (string, error) {
    priv, _, err := crypto.GenerateKeyPair(crypto.Ed25519, -1)
    if err != nil {
        return "", err
    }
    raw, err := crypto.MarshalPrivateKey(priv)
    if err != nil {
        return "", err
    }
    return base64.StdEncoding.EncodeToString(raw), nil
}

func PrivateKeyFromB64(encoded string) (crypto.PrivKey, error) {
    raw, err := base64.StdEncoding.DecodeString(encoded)
    if err != nil {
        return nil, err
    }
    return crypto.UnmarshalPrivateKey(raw)
}
```

`NewHostWithIdentity` already exists from Task 3. Do not duplicate host construction here; Task 3b only adds encoding/decoding helpers and app/core wiring for persisted keys.

- [ ] **Step 2: Add stable identity test**

Create `internal/net/identity_test.go`:

```go
package net_test

import (
    "testing"

    bnet "github.com/chad/bdpeer/internal/net"
)

func TestPrivateKeyRoundTrip(t *testing.T) {
    encoded, err := bnet.GenerateIdentityB64()
    if err != nil {
        t.Fatal(err)
    }
    first, err := bnet.PrivateKeyFromB64(encoded)
    if err != nil {
        t.Fatal(err)
    }
    second, err := bnet.PrivateKeyFromB64(encoded)
    if err != nil {
        t.Fatal(err)
    }
    if !first.GetPublic().Equals(second.GetPublic()) {
        t.Fatal("expected persisted private key to produce stable public identity")
    }
}
```

- [ ] **Step 3: Use persisted identity in core startup**

Before creating the host in `core.Service.Start`:

```go
if s.cfg.PrivateKeyB64 == "" {
    encoded, err := bnet.GenerateIdentityB64()
    if err != nil {
        return err
    }
    s.cfg.PrivateKeyB64 = encoded
    _ = s.cfg.Save(s.cfgPath)
}
priv, err := bnet.PrivateKeyFromB64(s.cfg.PrivateKeyB64)
if err != nil {
    return err
}
s.host, err = bnet.NewHostWithIdentity(ctx, s.cfg.Nickname, priv)
```

- [ ] **Step 4: Verify**

```bash
go test ./internal/net/... -run 'TestPrivateKeyRoundTrip|TestNewHost' -v
```

- [ ] **Step 5: Commit**

```bash
git add internal/net/ internal/config/ internal/core/
git commit -m "feat: persist libp2p identity across launches"
```

---

## Task 4: Discovery Manager Scaffold

**Files:**
- Create: `internal/discovery/manager.go`
- Create: `internal/discovery/manager_test.go`

The manager aggregates all discovery backends. Each backend calls `manager.Notify(DiscoveredPeer)` when a peer is found or lost. The manager deduplicates by `peer.ID` when available and falls back to the first multiaddr string. This preserves the libp2p send target while still allowing best-effort discovery from protocols that do not expose a peer ID yet.

Peers without a `peer.ID` are discovery/proximity-only entries. The UI may display them, but text/file send controls must require `activePeer.ID != ""` so the app does not try to open a libp2p stream to an unknown peer.

- [ ] **Step 1: Write failing test**

Create `internal/discovery/manager_test.go`:

```go
package discovery_test

import (
    "testing"
    "time"

    "github.com/chad/bdpeer/internal/discovery"
    "github.com/libp2p/go-libp2p/core/peer"
    "github.com/multiformats/go-multiaddr"
)

func TestManagerNotify(t *testing.T) {
    m := discovery.NewManager("alice")
    found := make(chan discovery.DiscoveredPeer, 4)
    m.OnPeerFound = func(p discovery.DiscoveredPeer) { found <- p }

    id := peer.ID("bob-peer")
    addr, _ := multiaddr.NewMultiaddr("/ip4/192.168.1.2/tcp/4001")

    m.Notify(discovery.DiscoveredPeer{ID: id, Nickname: "bob", Addrs: []multiaddr.Multiaddr{addr}, Source: "mdns"})
    m.Notify(discovery.DiscoveredPeer{ID: id, Nickname: "bob", Addrs: []multiaddr.Multiaddr{addr}, Source: "ssdp"}) // duplicate

    select {
    case p := <-found:
        if p.Nickname != "bob" {
            t.Errorf("got %q, want bob", p.Nickname)
        }
    case <-time.After(time.Second):
        t.Fatal("timeout")
    }

    // Second notify is a duplicate — should NOT fire again
    select {
    case <-found:
        t.Error("duplicate peer should not fire OnPeerFound again")
    case <-time.After(100 * time.Millisecond):
        // correct: no duplicate
    }
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/discovery/... -run TestManagerNotify
```

Expected: FAIL — package does not exist

- [ ] **Step 3: Implement manager**

Create `internal/discovery/manager.go`:

```go
package discovery

import (
    "sync"

    "github.com/libp2p/go-libp2p/core/peer"
    "github.com/multiformats/go-multiaddr"
)

type DiscoveredPeer struct {
    ID       peer.ID
    Nickname string
    Addrs    []multiaddr.Multiaddr
    Addr     string // optional fallback for non-libp2p proximity signals such as BLE
    Source   string // "mdns", "ssdp", "wsd", "ble", "dht"
}

type Manager struct {
    nickname    string
    mu          sync.Mutex
    seen        map[string]DiscoveredPeer // key: peer ID when present, otherwise first addr
    OnPeerFound func(DiscoveredPeer)
    OnPeerLost  func(DiscoveredPeer)
}

func NewManager(nickname string) *Manager {
    return &Manager{
        nickname: nickname,
        seen:     make(map[string]DiscoveredPeer),
    }
}

// Notify is called by any backend when a peer is discovered.
func (m *Manager) Notify(p DiscoveredPeer) {
    if p.Nickname == m.nickname {
        return // ignore self
    }
    key := peerKey(p)
    if key == "" {
        return
    }
    m.mu.Lock()
    _, exists := m.seen[key]
    if !exists {
        m.seen[key] = p
    }
    m.mu.Unlock()

    if !exists && m.OnPeerFound != nil {
        m.OnPeerFound(p)
    }
}

// Forget is called when a peer disappears (mDNS goodbye, BLE out-of-range).
func (m *Manager) Forget(key string) {
    m.mu.Lock()
    p, exists := m.seen[key]
    if exists {
        delete(m.seen, key)
    }
    m.mu.Unlock()

    if exists && m.OnPeerLost != nil {
        m.OnPeerLost(p)
    }
}

func peerKey(p DiscoveredPeer) string {
    if p.ID != "" {
        return p.ID.String()
    }
    if len(p.Addrs) > 0 {
        return p.Addrs[0].String()
    }
    if p.Addr != "" {
        return p.Source + ":" + p.Addr
    }
    return ""
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test ./internal/discovery/... -v
```

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/discovery/
git commit -m "feat: discovery manager with dedup across all backends"
```

---

## Task 4b: Bonjour/mDNS Backend (`_bdpeer._tcp`)

**Files:**
- Create: `internal/discovery/bonjour.go`
- Create: `internal/discovery/bonjour_test.go`
- Add dependency: `github.com/grandcat/zeroconf`

`grandcat/zeroconf` publishes a proper Bonjour service that iOS, macOS, Android (NSD), and Windows 10+ mDNS can discover. Service instance name encodes nickname: `<nickname>._bdpeer._tcp.local.`

- [ ] **Step 1: Add dependency**

```bash
go get github.com/grandcat/zeroconf@latest
```

- [ ] **Step 2: Write failing test**

Create `internal/discovery/bonjour_test.go`:

```go
package discovery_test

import (
    "context"
    "testing"
    "time"

    "github.com/chad/bdpeer/internal/discovery"
)

func TestBonjourRegisterAndBrowse(t *testing.T) {
    ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()

    found := make(chan discovery.DiscoveredPeer, 1)
    mgr := discovery.NewManager("alice")
    mgr.OnPeerFound = func(p discovery.DiscoveredPeer) { found <- p }

    // Register "bob" on port 5001
    svcBob, err := discovery.RegisterBonjour("bob", 5001, nil)
    if err != nil {
        t.Fatalf("RegisterBonjour: %v", err)
    }
    defer svcBob.Shutdown()

    // Browse from alice's manager
    stopBrowse, err := discovery.BrowseBonjour(ctx, mgr)
    if err != nil {
        t.Fatalf("BrowseBonjour: %v", err)
    }
    defer stopBrowse()

    select {
    case p := <-found:
        if p.Nickname != "bob" {
            t.Errorf("got %q, want bob", p.Nickname)
        }
        if p.Source != "mdns" {
            t.Errorf("source: got %q, want mdns", p.Source)
        }
    case <-ctx.Done():
        t.Fatal("timed out — mDNS not found on loopback. On Linux ensure avahi or systemd-resolved is running.")
    }
}
```

- [ ] **Step 3: Run test to verify it fails**

```bash
go test ./internal/discovery/... -run TestBonjourRegisterAndBrowse -timeout 15s
```

Expected: FAIL — `RegisterBonjour`, `BrowseBonjour` undefined

- [ ] **Step 4: Implement Bonjour backend**

Create `internal/discovery/bonjour.go`:

```go
package discovery

import (
    "context"
    "fmt"
    "net"
    "strings"

    "github.com/grandcat/zeroconf"
    "github.com/libp2p/go-libp2p/core/peer"
    "github.com/multiformats/go-multiaddr"
)

const bonjourService = "_bdpeer._tcp"
const bonjourDomain  = "local."

// RegisterBonjour advertises this node on the LAN.
// iOS/macOS Bonjour browsers, Android NSD, Windows 10+ mDNS will see it.
func RegisterBonjour(nickname string, port int, fullAddrs []multiaddr.Multiaddr) (*zeroconf.Server, error) {
    txt := []string{"txtv=0", "app=bdpeer"}
    for _, addr := range fullAddrs {
        txt = append(txt, "addr="+addr.String())
    }
    return zeroconf.Register(
        nickname,        // instance name (= nickname)
        bonjourService,
        bonjourDomain,
        port,
        txt,
        nil, // all interfaces
    )
}

// BrowseBonjour scans the LAN for _bdpeer._tcp services and notifies mgr.
// Returns a stop function and an error.
func BrowseBonjour(ctx context.Context, mgr *Manager) (stop func(), err error) {
    resolver, err := zeroconf.NewResolver(nil)
    if err != nil {
        return nil, fmt.Errorf("zeroconf resolver: %w", err)
    }

    entries := make(chan *zeroconf.ServiceEntry)
    go func() {
        for entry := range entries {
            if len(entry.AddrIPv4) == 0 && len(entry.AddrIPv6) == 0 {
                continue
            }
            var ip net.IP
            if len(entry.AddrIPv4) > 0 {
                ip = entry.AddrIPv4[0]
            } else {
                ip = entry.AddrIPv6[0]
            }
            ma := bonjourMultiaddr(entry, ip)
            var err error
            if ma == nil {
                ma, err = multiaddr.NewMultiaddr("/ip4/" + ip.String() + "/tcp/" + fmt.Sprint(entry.Port))
            }
            if err != nil {
                continue
            }
            var id peer.ID
            if info, err := peer.AddrInfoFromP2pAddr(ma); err == nil {
                id = info.ID
            }
            mgr.Notify(DiscoveredPeer{
                ID:       id,
                Nickname: entry.Instance,
                Addrs:    []multiaddr.Multiaddr{ma},
                Source:   "mdns",
            })
        }
    }()

    browseCtx, cancel := context.WithCancel(ctx)
    go func() {
        _ = resolver.Browse(browseCtx, bonjourService, bonjourDomain, entries)
    }()

    return cancel, nil
}

func bonjourMultiaddr(entry *zeroconf.ServiceEntry, ip net.IP) multiaddr.Multiaddr {
    for _, txt := range entry.Text {
        if strings.HasPrefix(txt, "addr=") {
            ma, err := multiaddr.NewMultiaddr(strings.TrimPrefix(txt, "addr="))
            if err == nil {
                return ma
            }
        }
    }
    return nil
}
```

- [ ] **Step 5: Run test to verify it passes**

```bash
go test ./internal/discovery/... -run TestBonjourRegisterAndBrowse -v -timeout 15s
```

Expected: PASS (requires mDNS on localhost — works on macOS/Linux with avahi)

- [ ] **Step 6: Commit**

```bash
git add internal/discovery/bonjour.go internal/discovery/bonjour_test.go go.mod go.sum
git commit -m "feat: Bonjour/mDNS backend via zeroconf (_bdpeer._tcp)"
```

---

## Task 4c: SSDP/UPnP Backend (Windows + Android)

**Files:**
- Create: `internal/discovery/ssdp.go`
- Create: `internal/discovery/ssdp_test.go`
- Add dependency: `github.com/koron/go-ssdp`

SSDP is used by Windows Network Discovery and some Android apps. We advertise `urn:bdpeer-org:device:BdPeer:1` and browse for it.

- [ ] **Step 1: Add dependency**

```bash
go get github.com/koron/go-ssdp@latest
```

- [ ] **Step 2: Write failing test**

Create `internal/discovery/ssdp_test.go`:

```go
package discovery_test

import (
    "context"
    "testing"
    "time"

    "github.com/chad/bdpeer/internal/discovery"
)

func TestSSDPAdvertiseAndSearch(t *testing.T) {
    ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
    defer cancel()

    found := make(chan discovery.DiscoveredPeer, 1)
    mgr := discovery.NewManager("alice")
    mgr.OnPeerFound = func(p discovery.DiscoveredPeer) { found <- p }

    // Advertise "bob" on port 5002
    stopAdv, err := discovery.AdvertiseSSDP(ctx, "bob", 5002)
    if err != nil {
        t.Fatalf("AdvertiseSSDP: %v", err)
    }
    defer stopAdv()

    time.Sleep(200 * time.Millisecond) // let advertisement settle

    stopSearch, err := discovery.SearchSSDP(ctx, mgr)
    if err != nil {
        t.Fatalf("SearchSSDP: %v", err)
    }
    defer stopSearch()

    select {
    case p := <-found:
        if p.Source != "ssdp" {
            t.Errorf("source: got %q, want ssdp", p.Source)
        }
    case <-ctx.Done():
        t.Skip("SSDP not received — may require multicast on this network interface")
    }
}
```

- [ ] **Step 3: Run test to verify it fails**

```bash
go test ./internal/discovery/... -run TestSSDPAdvertiseAndSearch -timeout 12s
```

Expected: FAIL — `AdvertiseSSDP`, `SearchSSDP` undefined

- [ ] **Step 4: Implement SSDP backend**

Create `internal/discovery/ssdp.go`:

```go
package discovery

import (
    "context"
    "fmt"
    "net/url"
    "strings"
    "time"

    "github.com/koron/go-ssdp"
    "github.com/multiformats/go-multiaddr"
)

const ssdpType = "urn:bdpeer-org:device:BdPeer:1"

// AdvertiseSSDP broadcasts this node via SSDP multicast (239.255.255.250:1900).
// Windows Network Discovery and some Android apps listen on this address.
func AdvertiseSSDP(ctx context.Context, nickname string, port int) (stop func(), err error) {
    ad, err := ssdp.Advertise(
        ssdpType,
        fmt.Sprintf("uuid:bdpeer-%s", nickname),
        fmt.Sprintf("http://0.0.0.0:%d/bdpeer.xml", port),
        fmt.Sprintf("bdpeer/%s", nickname),
        1800,
    )
    if err != nil {
        return nil, fmt.Errorf("ssdp advertise: %w", err)
    }

    go func() {
        ticker := time.NewTicker(30 * time.Second)
        defer ticker.Stop()
        for {
            select {
            case <-ctx.Done():
                ad.Close()
                return
            case <-ticker.C:
                ad.Alive()
            }
        }
    }()

    return func() { ad.Close() }, nil
}

// SearchSSDP issues an M-SEARCH and listens for SSDP responses.
func SearchSSDP(ctx context.Context, mgr *Manager) (stop func(), err error) {
    stopCtx, cancel := context.WithCancel(ctx)

    go func() {
        list, err := ssdp.Search(ssdpType, 3, "")
        if err != nil {
            cancel()
            return
        }
        for _, srv := range list {
            nickname := parseSSDPNickname(srv.Server)
            ma := parseSSDPAddr(srv.Location)
            if nickname == "" || ma == nil {
                continue
            }
            mgr.Notify(DiscoveredPeer{Nickname: nickname, Addrs: []multiaddr.Multiaddr{ma}, Source: "ssdp"})
        }

        // Also monitor for new announcements
        mon, err := ssdp.Monitor(nil)
        if err != nil {
            return
        }
        defer mon.Close()
        for {
            select {
            case <-stopCtx.Done():
                return
            default:
                alive, err := mon.Alive()
                if err != nil {
                    return
                }
                if alive.Type != ssdpType {
                    continue
                }
                nick := parseSSDPNickname(alive.Server)
                ma := parseSSDPAddr(alive.Location)
                if nick != "" && ma != nil {
                    mgr.Notify(DiscoveredPeer{Nickname: nick, Addrs: []multiaddr.Multiaddr{ma}, Source: "ssdp"})
                }
            }
        }
    }()

    return cancel, nil
}

func parseSSDPNickname(server string) string {
    // server field: "bdpeer/<nickname>"
    if idx := strings.Index(server, "bdpeer/"); idx >= 0 {
        return server[idx+7:]
    }
    return ""
}

func parseSSDPAddr(location string) multiaddr.Multiaddr {
    u, err := url.Parse(location)
    if err != nil {
        return nil
    }
    host := u.Hostname()
    port := u.Port()
    if host == "" || port == "" {
        return nil
    }
    ma, err := multiaddr.NewMultiaddr("/ip4/" + host + "/tcp/" + port)
    if err != nil {
        return nil
    }
    return ma
}
```

- [ ] **Step 5: Run test to verify it passes**

```bash
go test ./internal/discovery/... -run TestSSDPAdvertiseAndSearch -v -timeout 12s
```

Expected: PASS or SKIP (multicast may not work on all test environments)

- [ ] **Step 6: Commit**

```bash
git add internal/discovery/ssdp.go internal/discovery/ssdp_test.go go.mod go.sum
git commit -m "feat: SSDP/UPnP discovery backend for Windows and Android"
```

---

## Task 4d: WS-Discovery Backend (Windows Network Folder)

**Files:**
- Create: `internal/discovery/wsd.go`
- Create: `internal/discovery/wsd_test.go`

WS-Discovery (WSD) is what makes a device appear in Windows Explorer → Network. Uses UDP multicast on `239.255.255.250:3702`. We send a `Hello` probe; Windows sends `ProbeMatch` responses. No external library — we implement the minimal WSD subset in ~80 lines.

- [ ] **Step 1: Write failing test**

Create `internal/discovery/wsd_test.go`:

```go
package discovery_test

import (
    "context"
    "testing"
    "time"

    "github.com/chad/bdpeer/internal/discovery"
)

func TestWSDHello(t *testing.T) {
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()

    // Just verify Hello sends without error (WSD probe reception is network-dependent)
    err := discovery.SendWSDHello(ctx, "alice", 4001)
    if err != nil {
        t.Fatalf("SendWSDHello: %v", err)
    }
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/discovery/... -run TestWSDHello -timeout 8s
```

Expected: FAIL — `SendWSDHello` undefined

- [ ] **Step 3: Implement WSD Hello/Listen**

Create `internal/discovery/wsd.go`:

```go
package discovery

import (
    "context"
    "fmt"
    "net"
    "strings"
    "text/template"
    "bytes"
    "time"

    "github.com/google/uuid"
    "github.com/multiformats/go-multiaddr"
)

const wsdMulticast = "239.255.255.250:3702"

var wsdHelloTmpl = template.Must(template.New("wsd").Parse(`<?xml version="1.0" encoding="UTF-8"?>
<soap:Envelope xmlns:soap="http://www.w3.org/2003/05/soap-envelope"
               xmlns:wsa="http://schemas.xmlsoap.org/ws/2004/08/addressing"
               xmlns:wsd="http://schemas.xmlsoap.org/ws/2005/04/discovery"
               xmlns:wsdp="http://schemas.xmlsoap.org/ws/2006/02/devprof">
  <soap:Header>
    <wsa:Action>http://schemas.xmlsoap.org/ws/2005/04/discovery/Hello</wsa:Action>
    <wsa:MessageID>urn:uuid:{{.MsgID}}</wsa:MessageID>
    <wsa:To>urn:schemas-xmlsoap-org:ws:2005:04:discovery</wsa:To>
  </soap:Header>
  <soap:Body>
    <wsd:Hello>
      <wsa:EndpointReference>
        <wsa:Address>urn:uuid:{{.DeviceID}}</wsa:Address>
      </wsa:EndpointReference>
      <wsd:Types>wsdp:Device</wsd:Types>
      <wsd:XAddrs>http://{{.Addr}}/bdpeer/{{.Nickname}}</wsd:XAddrs>
      <wsd:MetadataVersion>1</wsd:MetadataVersion>
    </wsd:Hello>
  </soap:Body>
</soap:Envelope>`))

// SendWSDHello broadcasts a WSD Hello to the LAN.
// Windows machines listen on 3702 and will register us in their network cache.
func SendWSDHello(ctx context.Context, nickname string, port int) error {
    conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
    if err != nil {
        return fmt.Errorf("wsd udp: %w", err)
    }
    defer conn.Close()

    dst, err := net.ResolveUDPAddr("udp4", wsdMulticast)
    if err != nil {
        return err
    }

    localIP := localIPv4()
    var buf bytes.Buffer
    _ = wsdHelloTmpl.Execute(&buf, map[string]string{
        "MsgID":    uuid.NewString(),
        "DeviceID": uuid.NewString(),
        "Addr":     fmt.Sprintf("%s:%d", localIP, port),
        "Nickname": nickname,
    })

    conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
    _, err = conn.WriteToUDP(buf.Bytes(), dst)
    return err
}

// ListenWSD listens for WSD Hello/ProbeMatch messages and notifies mgr.
func ListenWSD(ctx context.Context, mgr *Manager) error {
    addr, err := net.ResolveUDPAddr("udp4", "0.0.0.0:3702")
    if err != nil {
        return err
    }
    conn, err := net.ListenUDP("udp4", addr)
    if err != nil {
        return fmt.Errorf("wsd listen 3702: %w (may need firewall rule on Windows)", err)
    }

    go func() {
        defer conn.Close()
        buf := make([]byte, 8192)
        for {
            select {
            case <-ctx.Done():
                return
            default:
            }
            conn.SetReadDeadline(time.Now().Add(time.Second))
            n, remote, err := conn.ReadFromUDP(buf)
            if err != nil {
                continue
            }
            body := string(buf[:n])
            // Extract XAddrs for the address, nick from XAddrs path
            if strings.Contains(body, "Hello") || strings.Contains(body, "ProbeMatch") {
                addr := extractXAddr(body, remote.String())
                nick := extractWSDNick(body)
                host, port, splitErr := net.SplitHostPort(addr)
                ma, err := multiaddr.NewMultiaddr("/ip4/" + host + "/tcp/" + port)
                if addr != "" && splitErr == nil && err == nil {
                    mgr.Notify(DiscoveredPeer{Nickname: nick, Addrs: []multiaddr.Multiaddr{ma}, Source: "wsd"})
                }
            }
        }
    }()
    return nil
}

func extractXAddr(body, fallback string) string {
    start := strings.Index(body, "<wsd:XAddrs>")
    end := strings.Index(body, "</wsd:XAddrs>")
    if start < 0 || end < 0 {
        return fallback
    }
    raw := body[start+12 : end]
    // raw is like "http://1.2.3.4:4001/bdpeer"
    raw = strings.TrimPrefix(raw, "http://")
    if idx := strings.Index(raw, "/"); idx >= 0 {
        raw = raw[:idx]
    }
    return raw
}

func extractWSDNick(body string) string {
    // We embed nickname in the XAddrs path — not standard WSD but harmless
    start := strings.Index(body, "/bdpeer/")
    if start < 0 {
        return "unknown"
    }
    end := strings.Index(body[start+8:], "<")
    if end < 0 {
        return body[start+8:]
    }
    return body[start+8 : start+8+end]
}

func localIPv4() string {
    conn, err := net.Dial("udp4", "8.8.8.8:80")
    if err != nil {
        return "127.0.0.1"
    }
    defer conn.Close()
    return conn.LocalAddr().(*net.UDPAddr).IP.String()
}
```

Add `github.com/google/uuid` dependency:

```bash
go get github.com/google/uuid@latest
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test ./internal/discovery/... -run TestWSDHello -v -timeout 8s
```

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/discovery/wsd.go internal/discovery/wsd_test.go go.mod go.sum
git commit -m "feat: WS-Discovery backend for Windows Network folder visibility"
```

---

## Task 4e: BLE Advertisement Backend (Proximity Discovery)

**Files:**
- Create: `internal/discovery/ble_stub.go`
- Create: `internal/discovery/ble_darwin.go`
- Create: `internal/discovery/ble_linux.go`
- Create: `internal/discovery/ble_windows.go`
- Add dependency: `tinygo.org/x/bluetooth`

BLE is used by AirDrop and Quick Share as the first stage of discovery (before WiFi transfer). We advertise a custom service UUID and scan for other `bdpeer` peripherals. Build tag `ble` enables this; default build omits it (avoids CGo requirement for CI).

- [ ] **Step 1: Add dependency**

```bash
go get tinygo.org/x/bluetooth@latest
```

- [ ] **Step 2: Create BLE interface stub (default build)**

Create `internal/discovery/ble_stub.go`:

```go
//go:build !ble

package discovery

import "context"

// BLEBackend is a no-op when compiled without -tags ble.
func StartBLE(ctx context.Context, nickname string, mgr *Manager) error {
    return nil // BLE not compiled in; use -tags ble to enable
}
```

- [ ] **Step 3: Implement BLE for macOS**

Create `internal/discovery/ble_darwin.go`:

```go
//go:build ble && darwin

package discovery

import (
    "context"
    "fmt"

    "tinygo.org/x/bluetooth"
)

// bdpeerServiceUUID is a random stable UUID for bdpeer BLE advertisement.
var bdpeerServiceUUID = bluetooth.NewUUID([16]byte{
    0xbd, 0x0e, 0xee, 0x00, 0x00, 0x01, 0x11, 0xee,
    0x80, 0x00, 0x00, 0x80, 0x5f, 0x9b, 0x34, 0xfb,
})

// StartBLE starts BLE advertising (peripheral) and scanning (central).
// Requires Bluetooth permission on macOS (Info.plist NSBluetoothAlwaysUsageDescription).
func StartBLE(ctx context.Context, nickname string, mgr *Manager) error {
    adapter := bluetooth.DefaultAdapter
    if err := adapter.Enable(); err != nil {
        return fmt.Errorf("BLE enable: %w", err)
    }

    // Advertise — encode nickname as manufacturer data (max 26 bytes)
    nick := []byte(nickname)
    if len(nick) > 26 {
        nick = nick[:26]
    }
    adv := adapter.DefaultAdvertisement()
    adv.Configure(bluetooth.AdvertisementOptions{
        LocalName:    "bdpeer-" + nickname,
        ServiceUUIDs: []bluetooth.UUID{bdpeerServiceUUID},
        ManufacturerData: []bluetooth.ManufacturerDataElement{{
            CompanyID: 0xFFFF,
            Data:      nick,
        }},
    })
    go adv.Start()

    // Scan for other bdpeer devices
    go func() {
        _ = adapter.Scan(func(adapter *bluetooth.Adapter, device bluetooth.ScanResult) {
            select {
            case <-ctx.Done():
                adapter.StopScan()
                return
            default:
            }
            for _, svcUUID := range device.ServiceUUIDs {
                if svcUUID == bdpeerServiceUUID {
                    nick := extractBLENickname(device.ManufacturerData())
                    mgr.Notify(DiscoveredPeer{
                        Nickname: nick,
                        Addr:     device.Address.String(),
                        Source:   "ble",
                    })
                }
            }
        })
    }()

    <-ctx.Done()
    return nil
}

func extractBLENickname(data []bluetooth.ManufacturerDataElement) string {
    for _, d := range data {
        if d.CompanyID == 0xFFFF && len(d.Data) > 0 {
            return string(d.Data)
        }
    }
    return "unknown"
}
```

- [ ] **Step 4: Implement BLE for Linux**

Create `internal/discovery/ble_linux.go`:

```go
//go:build ble && linux

package discovery

import (
    "context"
    "fmt"

    "tinygo.org/x/bluetooth"
)

func StartBLE(ctx context.Context, nickname string, mgr *Manager) error {
    adapter := bluetooth.DefaultAdapter
    if err := adapter.Enable(); err != nil {
        return fmt.Errorf("BLE enable (ensure bluetoothd is running): %w", err)
    }
    go func() {
        _ = adapter.Scan(func(a *bluetooth.Adapter, d bluetooth.ScanResult) {
            select {
            case <-ctx.Done():
                a.StopScan()
                return
            default:
            }
            nick := extractBLENickname(d.ManufacturerData())
            if nick != "unknown" {
                mgr.Notify(DiscoveredPeer{Nickname: nick, Addr: d.Address.String(), Source: "ble"})
            }
        })
    }()
    <-ctx.Done()
    return nil
}

func extractBLENickname(data []bluetooth.ManufacturerDataElement) string {
    for _, d := range data {
        if d.CompanyID == 0xFFFF && len(d.Data) > 0 {
            return string(d.Data)
        }
    }
    return "unknown"
}
```

- [ ] **Step 5: Implement BLE for Windows**

Create `internal/discovery/ble_windows.go`:

```go
//go:build ble && windows

package discovery

import (
    "context"
    "fmt"

    "tinygo.org/x/bluetooth"
)

func StartBLE(ctx context.Context, nickname string, mgr *Manager) error {
    adapter := bluetooth.DefaultAdapter
    if err := adapter.Enable(); err != nil {
        return fmt.Errorf("BLE enable (ensure Bluetooth is on): %w", err)
    }
    go func() {
        _ = adapter.Scan(func(a *bluetooth.Adapter, d bluetooth.ScanResult) {
            select {
            case <-ctx.Done():
                a.StopScan()
                return
            default:
            }
            nick := extractBLENickname(d.ManufacturerData())
            if nick != "unknown" {
                mgr.Notify(DiscoveredPeer{Nickname: nick, Addr: d.Address.String(), Source: "ble"})
            }
        })
    }()
    <-ctx.Done()
    return nil
}

func extractBLENickname(data []bluetooth.ManufacturerDataElement) string {
    for _, d := range data {
        if d.CompanyID == 0xFFFF && len(d.Data) > 0 {
            return string(d.Data)
        }
    }
    return "unknown"
}
```

- [ ] **Step 6: Update Makefile with BLE build targets**

Add to `Makefile`:

```makefile
build-mac-ble:
	GOOS=darwin GOARCH=arm64 go build -tags ble -o dist/bdpeer-mac-arm64-ble ./cmd/bdpeer

build-linux-ble:
	GOOS=linux GOARCH=amd64 go build -tags ble -o dist/bdpeer-linux-amd64-ble ./cmd/bdpeer

build-win-ble:
	GOOS=windows GOARCH=amd64 go build -tags ble -o dist/bdpeer-windows-amd64-ble.exe ./cmd/bdpeer
```

- [ ] **Step 7: Verify default build still works (no BLE)**

```bash
go build ./...
```

Expected: no errors (stub file used, no CGo)

- [ ] **Step 8: Commit**

```bash
git add internal/discovery/ble_*.go Makefile go.mod go.sum
git commit -m "feat: BLE discovery backend with build-tag guard (ble_stub default)"
```

---

## Task 4f: Wire All Discovery Backends + Update net/host.go

**Files:**
- Modify: `internal/net/host.go` (remove old mDNS, use discovery.Manager)
- Create/Modify: `internal/core/service.go`

This is the first concrete `bdpeer-core` integration point. If `internal/core/service.go` does not exist yet, create the minimal `Service`, `Event`, and `SendRequest` shape from Task 7b before wiring discovery here.

- [ ] **Step 1: Add discovery attachment without changing `NewHost`**

Keep `NewHost(ctx, nickname)` stable. Add a method that attaches discovery notifications after the host exists.

Add these imports to `internal/net/host.go`:

```go
import (
    "github.com/chad/bdpeer/internal/discovery"
    "github.com/libp2p/go-libp2p/core/network"
)
```

```go
func (h *Host) AttachDiscovery(mgr *discovery.Manager) {
    h.Libp2p.Network().Notify(&libp2pNotifee{host: h, mgr: mgr})
}

type libp2pNotifee struct {
    host *Host
    mgr  *discovery.Manager
}

func (n *libp2pNotifee) Connected(_ network.Network, conn network.Conn) {
    id := conn.RemotePeer()
    nick := n.host.nicknameFor(id)
    addrs := n.host.Libp2p.Peerstore().Addrs(id)
    n.mgr.Notify(discovery.DiscoveredPeer{ID: id, Nickname: nick, Addrs: addrs, Source: "dht"})
}
func (n *libp2pNotifee) Disconnected(_ network.Network, conn network.Conn) {
    n.mgr.Forget(conn.RemotePeer().String())
}
func (n *libp2pNotifee) Listen(_ network.Network, _ multiaddr.Multiaddr)      {}
func (n *libp2pNotifee) ListenClose(_ network.Network, _ multiaddr.Multiaddr) {}
```

- [ ] **Step 2: Wire discovery inside core.Service.Start**

Inside `core.Service.Start`, create the discovery manager after the host exists and emit core events:

```go
mgr := discovery.NewManager(s.cfg.Nickname)
mgr.OnPeerFound = func(p discovery.DiscoveredPeer) {
    s.events <- Event{Type: EventPeerFound, Peer: bnet.PeerInfo{
        ID: p.ID, Nickname: p.Nickname, Addrs: p.Addrs, Source: p.Source,
    }}
}
mgr.OnPeerLost = func(p discovery.DiscoveredPeer) {
    s.events <- Event{Type: EventPeerLost, Peer: bnet.PeerInfo{ID: p.ID}}
}

s.host, err = bnet.NewHost(ctx, s.cfg.Nickname)
// ...
s.host.AttachDiscovery(mgr)
s.host.StartStreamHandler() // required before any peer can deliver frames

// Start all discovery backends
if bonjourStop, err := discovery.BrowseBonjour(ctx, mgr); err == nil {
    s.stops = append(s.stops, bonjourStop)
    if server, err := discovery.RegisterBonjour(s.cfg.Nickname, listenPort(s.host), hostFullAddrs(s.host)); err == nil {
        s.stops = append(s.stops, server.Shutdown)
    }
}
if ssdpStop, err := discovery.SearchSSDP(ctx, mgr); err == nil {
    s.stops = append(s.stops, ssdpStop)
    if advStop, err := discovery.AdvertiseSSDP(ctx, s.cfg.Nickname, listenPort(s.host)); err == nil {
        s.stops = append(s.stops, advStop)
    }
}
// WSD is best-effort: bind on UDP/3702 can fail (Windows firewall, port in use).
// We intentionally swallow these errors so other discovery backends keep working.
// If you need visibility, emit EventError with a wrapped error instead.
_ = discovery.ListenWSD(ctx, mgr)
_ = discovery.SendWSDHello(ctx, s.cfg.Nickname, listenPort(s.host))
go discovery.StartBLE(ctx, s.cfg.Nickname, mgr) // no-op without -tags ble
```

Add the following imports to `internal/core/service.go` for the helpers below:

```go
import (
    "fmt"

    "github.com/multiformats/go-multiaddr"
    maddr "github.com/multiformats/go-multiaddr"
)
```

Add helper:

```go
func listenPort(h *bnet.Host) int {
    for _, a := range h.Libp2p.Addrs() {
        if portStr, err := a.ValueForProtocol(maddr.P_TCP); err == nil {
            var port int
            fmt.Sscanf(portStr, "%d", &port)
            return port
        }
    }
    return 4001
}

func hostFullAddrs(h *bnet.Host) []multiaddr.Multiaddr {
    out := make([]multiaddr.Multiaddr, 0, len(h.Libp2p.Addrs()))
    suffix, _ := multiaddr.NewMultiaddr("/p2p/" + h.Libp2p.ID().String())
    for _, a := range h.Libp2p.Addrs() {
        if _, err := a.ValueForProtocol(maddr.P_TCP); err == nil {
            out = append(out, a.Encapsulate(suffix))
        }
    }
    return out
}
```

- [ ] **Step 3: Keep UI/core events on `net.PeerInfo`**

Do not change `MsgPeerFound` or core peer events to raw discovery fields. Keep:

```go
type MsgPeerFound struct{ Info bnet.PeerInfo }
type MsgPeerLost  struct{ ID peer.ID }
```

Update `appendOrUpdate` to key on `peer.ID` when present; fall back to the first multiaddr string only for discovery signals that do not include a peer ID.

- [ ] **Step 4: Build**

```bash
go build ./...
```

Expected: no errors

- [ ] **Step 5: Commit**

```bash
git add internal/core/ internal/net/ internal/discovery/
git commit -m "feat: wire discovery backends through bdpeer-core"
```

---

## Task 5: Stream Protocol + Text Messaging

**Files:**
- Create: `internal/proto/protocol.go`
- Create: `internal/transfer/message.go`
- Create: `internal/transfer/message_test.go`
- Create: `internal/net/stream.go`

- [ ] **Step 1: Write failing test for frame codec**

Create `internal/transfer/message_test.go`:

```go
package transfer_test

import (
    "bytes"
    "testing"

    "github.com/chad/bdpeer/internal/proto"
    "github.com/chad/bdpeer/internal/transfer"
)

func TestWriteReadTextFrame(t *testing.T) {
    buf := &bytes.Buffer{}
    frame := proto.Frame{
        Type:    proto.FrameText,
        From:    "alice",
        Content: "hello bob",
    }

    if err := transfer.WriteFrame(buf, frame); err != nil {
        t.Fatalf("WriteFrame: %v", err)
    }

    got, err := transfer.ReadFrame(buf)
    if err != nil {
        t.Fatalf("ReadFrame: %v", err)
    }
    if got.Content != "hello bob" {
        t.Errorf("got %q, want %q", got.Content, "hello bob")
    }
    if got.From != "alice" {
        t.Errorf("got from %q, want alice", got.From)
    }
}

func TestWriteReadMultipleFrames(t *testing.T) {
    buf := &bytes.Buffer{}
    frames := []proto.Frame{
        {Type: proto.FrameText, From: "a", Content: "one"},
        {Type: proto.FrameText, From: "b", Content: "two"},
    }
    for _, f := range frames {
        if err := transfer.WriteFrame(buf, f); err != nil {
            t.Fatal(err)
        }
    }
    for _, want := range frames {
        got, err := transfer.ReadFrame(buf)
        if err != nil {
            t.Fatal(err)
        }
        if got.Content != want.Content {
            t.Errorf("got %q, want %q", got.Content, want.Content)
        }
    }
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/transfer/... -run TestWriteReadTextFrame
```

Expected: FAIL

- [ ] **Step 3: Implement frame codec**

Create `internal/transfer/message.go`:

```go
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
    if length > 64*1024*1024 { // sanity: 64 MB max
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
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test ./internal/transfer/... -v
```

Expected: PASS (2 tests)

- [ ] **Step 5: Add stream handler to Host**

Create `internal/net/stream.go`:

```go
package net

import (
    "bufio"
    "context"
    "fmt"

    "github.com/chad/bdpeer/internal/proto"
    "github.com/chad/bdpeer/internal/transfer"
    "github.com/libp2p/go-libp2p/core/network"
    "github.com/libp2p/go-libp2p/core/peer"
)

// OnFrameReceived is called for every incoming frame. Set before StartStreamHandler.
// (host.go Host struct — add this field in host.go)

// StartStreamHandler registers the /bdpeer/1.0.0 stream handler.
func (h *Host) StartStreamHandler() {
    h.Libp2p.SetStreamHandler(proto.Protocol, func(s network.Stream) {
        defer s.Close()
        r := bufio.NewReader(s)
        for {
            frame, err := transfer.ReadFrame(r)
            if err != nil {
                return // stream closed or error
            }
            if h.OnFrame != nil {
                h.OnFrame(s.Conn().RemotePeer(), frame)
            }
        }
    })
}

// SendFrame opens a stream to peerID and sends one frame.
func (h *Host) SendFrame(ctx context.Context, peerID peer.ID, frame proto.Frame) error {
    s, err := h.Libp2p.NewStream(ctx, peerID, proto.Protocol)
    if err != nil {
        return fmt.Errorf("open stream to %s: %w", peerID, err)
    }
    defer s.Close()
    return transfer.WriteFrame(s, frame)
}
```

- [ ] **Step 6: Add OnFrame field to Host struct in host.go**

In `internal/net/host.go`, add to `Host` struct:

```go
type Host struct {
    Libp2p      host.Host
    Nickname    string
    OnFrame     func(peer.ID, proto.Frame)   // add this line
    dht         *dht.IpfsDHT
}
```

Also add imports in host.go: `"github.com/libp2p/go-libp2p/core/peer"` and `"github.com/chad/bdpeer/internal/proto"`.

- [ ] **Step 7: Verify build**

```bash
go build ./...
```

Expected: no errors

- [ ] **Step 8: Commit**

```bash
git add internal/proto/ internal/transfer/ internal/net/stream.go internal/net/host.go
git commit -m "feat: frame codec and stream handler for text messages"
```

---

## Task 6: File Transfer

**Files:**
- Create: `internal/transfer/file.go`
- Create: `internal/transfer/file_test.go`

- [ ] **Step 1: Write failing test**

Create `internal/transfer/file_test.go`:

```go
package transfer_test

import (
    "bytes"
    "crypto/rand"
    "os"
    "path/filepath"
    "testing"

    "github.com/chad/bdpeer/internal/transfer"
)

func TestFileSendReceive(t *testing.T) {
    // Create a temp file with 100 KB of random data
    data := make([]byte, 100*1024)
    rand.Read(data)
    srcPath := filepath.Join(t.TempDir(), "source.bin")
    if err := os.WriteFile(srcPath, data, 0o644); err != nil {
        t.Fatal(err)
    }

    // Encode file to frames
    var buf bytes.Buffer
    if err := transfer.WriteFile(srcPath, "alice", &buf); err != nil {
        t.Fatalf("WriteFile: %v", err)
    }

    // Decode frames back to file
    destDir := t.TempDir()
    progress := make([]int64, 0)
    dest, err := transfer.ReadFile(&buf, destDir, func(recv, total int64) {
        progress = append(progress, recv)
    })
    if err != nil {
        t.Fatalf("ReadFile: %v", err)
    }

    got, err := os.ReadFile(dest)
    if err != nil {
        t.Fatal(err)
    }
    if !bytes.Equal(got, data) {
        t.Error("received file content does not match source")
    }
    if len(progress) == 0 {
        t.Error("expected at least one progress callback")
    }
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/transfer/... -run TestFileSendReceive
```

Expected: FAIL — `WriteFile` and `ReadFile` undefined

- [ ] **Step 3: Implement file transfer**

Create `internal/transfer/file.go`:

```go
package transfer

import (
    "crypto/sha256"
    "fmt"
    "io"
    "os"
    "path/filepath"

    "github.com/chad/bdpeer/internal/proto"
)

// WriteFile sends FILE_START, FILE_CHUNK..., FILE_END frames to w.
func WriteFile(srcPath, from string, w io.Writer) error {
    f, err := os.Open(srcPath)
    if err != nil {
        return err
    }
    defer f.Close()

    info, err := f.Stat()
    if err != nil {
        return err
    }

    if err := WriteFrame(w, proto.Frame{
        Type: proto.FrameFileStart,
        From: from,
        Name: filepath.Base(srcPath),
        Size: info.Size(),
    }); err != nil {
        return err
    }

    h := sha256.New()
    buf := make([]byte, proto.ChunkSize)
    seq := 0
    for {
        n, err := f.Read(buf)
        if n > 0 {
            chunk := make([]byte, n)
            copy(chunk, buf[:n])
            h.Write(chunk)
            if err := WriteFrame(w, proto.Frame{
                Type: proto.FrameFileChunk,
                Seq:  seq,
                Data: chunk,
            }); err != nil {
                return err
            }
            seq++
        }
        if err == io.EOF {
            break
        }
        if err != nil {
            return err
        }
    }

    return WriteFrame(w, proto.Frame{
        Type:     proto.FrameFileEnd,
        Checksum: fmt.Sprintf("sha256:%x", h.Sum(nil)),
    })
}

// ReadFile reads FILE_START, FILE_CHUNK..., FILE_END frames from r into destDir.
// progress is called after each chunk with (bytesReceived, totalBytes).
// Returns the path of the written file.
func ReadFile(r io.Reader, destDir string, progress func(int64, int64)) (string, error) {
    // Expect FILE_START
    startFrame, err := ReadFrame(r)
    if err != nil {
        return "", fmt.Errorf("read FILE_START: %w", err)
    }
    if startFrame.Type != proto.FrameFileStart {
        return "", fmt.Errorf("expected FILE_START, got %s", startFrame.Type)
    }

    destPath := filepath.Join(destDir, filepath.Base(startFrame.Name))
    f, err := os.Create(destPath)
    if err != nil {
        return "", err
    }
    defer f.Close()

    h := sha256.New()
    var received int64
    for {
        frame, err := ReadFrame(r)
        if err != nil {
            return "", fmt.Errorf("read chunk: %w", err)
        }
        if frame.Type == proto.FrameFileEnd {
            expected := fmt.Sprintf("sha256:%x", h.Sum(nil))
            if frame.Checksum != expected {
                os.Remove(destPath)
                return "", fmt.Errorf("checksum mismatch: got %s, want %s", frame.Checksum, expected)
            }
            return destPath, nil
        }
        if frame.Type != proto.FrameFileChunk {
            return "", fmt.Errorf("expected FILE_CHUNK, got %s", frame.Type)
        }
        if _, err := f.Write(frame.Data); err != nil {
            return "", err
        }
        h.Write(frame.Data)
        received += int64(len(frame.Data))
        if progress != nil {
            progress(received, startFrame.Size)
        }
    }
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test ./internal/transfer/... -v
```

Expected: PASS (3 tests)

- [ ] **Step 5: Commit**

```bash
git add internal/transfer/file.go internal/transfer/file_test.go
git commit -m "feat: chunked file transfer with SHA-256 checksum verification"
```

---

## Task 7: Direct IP Connection

**Files:**
- Modify: `internal/net/host.go`
- Create: `internal/net/connect_test.go`

- [ ] **Step 1: Write failing test**

Create `internal/net/connect_test.go`:

```go
package net_test

import (
    "context"
    "testing"
    "time"

    bnet "github.com/chad/bdpeer/internal/net"
)

func TestConnectByMultiaddr(t *testing.T) {
    ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()

    h1, err := bnet.NewHost(ctx, "alice")
    if err != nil {
        t.Fatal(err)
    }
    defer h1.Close()

    h2, err := bnet.NewHost(ctx, "bob")
    if err != nil {
        t.Fatal(err)
    }
    defer h2.Close()

    // Get h2's first TCP multiaddr
    addrStr := bnet.FirstTCPAddr(h2)
    if addrStr == "" {
        t.Fatal("h2 has no TCP addresses")
    }

    info, err := h1.ConnectByAddr(ctx, addrStr)
    if err != nil {
        t.Fatalf("ConnectByAddr: %v", err)
    }
    if info.ID != h2.Libp2p.ID() {
        t.Errorf("connected to wrong peer")
    }
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/net/... -run TestConnectByMultiaddr -timeout 15s
```

Expected: FAIL

- [ ] **Step 3: Implement ConnectByAddr**

Add to `internal/net/host.go`:

```go
import (
    // existing imports...
    "github.com/multiformats/go-multiaddr"
    maddr "github.com/multiformats/go-multiaddr"
)

// ConnectByAddr connects to a peer by full multiaddr string (e.g., /ip4/1.2.3.4/tcp/4001/p2p/QmXxx)
// or bare host:port which is wrapped as /ip4/<host>/tcp/<port>.
func (h *Host) ConnectByAddr(ctx context.Context, addrStr string) (PeerInfo, error) {
    ma, err := multiaddr.NewMultiaddr(addrStr)
    if err != nil {
        return PeerInfo{}, fmt.Errorf("parse addr %q: %w", addrStr, err)
    }
    pi, err := peer.AddrInfoFromP2pAddr(ma)
    if err != nil {
        return PeerInfo{}, fmt.Errorf("addr info from %q: %w", addrStr, err)
    }
    if err := h.Libp2p.Connect(ctx, *pi); err != nil {
        return PeerInfo{}, fmt.Errorf("connect: %w", err)
    }
    nickname := h.nicknameFor(pi.ID)
    return PeerInfo{ID: pi.ID, Nickname: nickname, Addrs: pi.Addrs}, nil
}

// FirstTCPAddr returns the first TCP listen multiaddr of h (with peer ID appended).
func FirstTCPAddr(h *Host) string {
    pid := h.Libp2p.ID()
    for _, a := range h.Libp2p.Addrs() {
        if _, err := a.ValueForProtocol(maddr.P_TCP); err == nil {
            full := a.String() + "/p2p/" + pid.String()
            return full
        }
    }
    return ""
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test ./internal/net/... -run TestConnectByMultiaddr -v -timeout 15s
```

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/net/
git commit -m "feat: connect by multiaddr for direct IP connections"
```

---

## Task 7b: bdpeer-core Service Facade

**Files:**
- Create: `internal/core/service.go`
- Create: `internal/core/service_test.go`

This task creates the reusable `bdpeer-core` boundary before any TUI work. The core is responsible for networking and transfer behavior; the TUI is only an input/output adapter.

- [ ] **Step 1: Define core API**

Create `internal/core/service.go`:

```go
package core

import (
    "context"

    "github.com/chad/bdpeer/internal/config"
    bnet "github.com/chad/bdpeer/internal/net"
    "github.com/libp2p/go-libp2p/core/peer"
)

type EventType string

const (
    EventPeerFound    EventType = "peer_found"
    EventPeerLost     EventType = "peer_lost"
    EventTextReceived EventType = "text_received"
    EventFileStart    EventType = "file_start"
    EventFileProgress EventType = "file_progress"
    EventFileDone     EventType = "file_done"
    EventError        EventType = "error"
)

type Event struct {
    Type     EventType
    Peer     bnet.PeerInfo
    From     string
    Content  string
    Name     string
    Size     int64
    Received int64
    Total    int64
    Path     string
    Err      error
}

type SendRequest struct {
    To      peer.ID
    Content string
    File    string
}

type Service struct {
    cfg        *config.Config
    cfgPath    string
    host       *bnet.Host
    recvDir    string
    stops      []func()
    events     chan Event
}

func NewService(cfg *config.Config, cfgPath string) *Service {
    return &Service{cfg: cfg, cfgPath: cfgPath, events: make(chan Event, 256)}
}

// Progress events should be throttled/coalesced for large files so a slow UI
// does not block file transfer on every 32 KB chunk.

func (s *Service) Events() <-chan Event { return s.events }

func (s *Service) Start(ctx context.Context) error {
    // Own all host/discovery setup here:
    // - load or create persisted libp2p identity
    // - create bnet.Host
    // - attach discovery.Manager
    // - register stream/file handlers
    // - start Bonjour/SSDP/WSD/BLE discovery
    <-ctx.Done()
    return s.Stop()
}

func (s *Service) Stop() error {
    for i := len(s.stops) - 1; i >= 0; i-- {
        s.stops[i]()
    }
    if s.host != nil {
        return s.host.Close()
    }
    return nil
}

func (s *Service) Send(ctx context.Context, req SendRequest) error {
    // Own text/file send here. TUI should not call bnet.Host directly.
    return nil
}
```

- [ ] **Step 2: Add core smoke test**

Create `internal/core/service_test.go`:

```go
package core_test

import (
    "testing"

    "github.com/chad/bdpeer/internal/config"
    "github.com/chad/bdpeer/internal/core"
)

func TestNewService(t *testing.T) {
    svc := core.NewService(&config.Config{Nickname: "alice"}, "")
    if svc.Events() == nil {
        t.Fatal("expected events channel")
    }
}
```

- [ ] **Step 3: Move wiring ownership into core**

When Task 11 wires the TUI, do not duplicate host/discovery setup in `cmd/bdpeer/main.go`. Move the `runNetwork` logic into `core.Service.Start` and expose only:

```go
svc := core.NewService(cfg, cfgPath)
go func() {
    if err := svc.Start(ctx); err != nil {
        prog.Send(ui.MsgError{Err: err})
    }
}()
go forwardCoreEvents(svc.Events(), prog)
```

The TUI sends user actions through:

```go
_ = svc.Send(ctx, core.SendRequest{To: peerID, Content: content})
```

- [ ] **Step 4: Verify**

```bash
go test ./internal/core/... ./internal/net/... ./internal/discovery/... ./internal/transfer/... -timeout 30s
```

- [ ] **Step 5: Commit**

```bash
git add internal/core/
git commit -m "feat: add bdpeer-core service facade"
```

---

## Task 8: TUI Styles and Root Model

**Files:**
- Create: `internal/ui/styles.go`
- Create: `internal/ui/app.go`

- [ ] **Step 1: Define styles**

Create `internal/ui/styles.go`:

```go
package ui

import "github.com/charmbracelet/lipgloss"

var (
    colorBase     = lipgloss.Color("#2E3440")
    colorSurface  = lipgloss.Color("#3B4252")
    colorPrimary  = lipgloss.Color("#88C0D0")
    colorSecond   = lipgloss.Color("#81A1C1")
    colorGreen    = lipgloss.Color("#A3BE8C")
    colorRed      = lipgloss.Color("#BF616A")
    colorText     = lipgloss.Color("#ECEFF4")
    colorMuted    = lipgloss.Color("#4C566A")

    StyleTitle = lipgloss.NewStyle().
            Foreground(colorPrimary).
            Bold(true).
            Padding(0, 1)

    StylePanel = lipgloss.NewStyle().
            Border(lipgloss.RoundedBorder()).
            BorderForeground(colorMuted)

    StylePeerOnline = lipgloss.NewStyle().
            Foreground(colorGreen)

    StylePeerOffline = lipgloss.NewStyle().
            Foreground(colorMuted)

    StyleMessage = lipgloss.NewStyle().
            Foreground(colorText)

    StyleMessageMine = lipgloss.NewStyle().
            Foreground(colorSecond)

    StyleInput = lipgloss.NewStyle().
            Foreground(colorText).
            BorderStyle(lipgloss.NormalBorder()).
            BorderTop(true).
            BorderForeground(colorMuted)

    StyleHelp = lipgloss.NewStyle().
            Foreground(colorMuted).
            Italic(true)

    StyleError = lipgloss.NewStyle().
            Foreground(colorRed)
)
```

- [ ] **Step 2: Define root Bubble Tea model**

Create `internal/ui/app.go`:

```go
package ui

import (
    "github.com/charmbracelet/bubbletea"
    bnet "github.com/chad/bdpeer/internal/net"
    "github.com/libp2p/go-libp2p/core/peer"
)

type Screen int

const (
    ScreenSetup Screen = iota
    ScreenMain
)

// Bubble Tea messages dispatched from goroutines
type MsgPeerFound    struct{ Info bnet.PeerInfo }
type MsgPeerLost     struct{ ID peer.ID }
type MsgTextReceived struct{ From, Content string }
type MsgFileStart    struct{ From, Name string; Size int64 }
type MsgFileProgress struct{ From string; Received, Total int64 }
type MsgFileDone     struct{ From, Name, SavePath string }
type MsgError        struct{ Err error }

type Message struct {
    From    string
    Content string
    Mine    bool
}

type Model struct {
    screen      Screen
    nickname    string
    peers       []bnet.PeerInfo
    activePeer  *bnet.PeerInfo
    messages    []Message
    inputBuf    string
    width       int
    height      int
    err         error
    fileXfer    *fileXfer
}

type fileXfer struct {
    From     string
    Name     string
    Received int64
    Total    int64
    Done     bool
    SavePath string
}

func New(nickname string) Model {
    screen := ScreenMain
    if nickname == "" {
        screen = ScreenSetup
    }
    return Model{screen: screen, nickname: nickname}
}

func (m Model) Init() tea.Cmd { return nil }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
    switch msg := msg.(type) {
    case tea.KeyMsg:
        return m.handleKey(msg)
    case tea.WindowSizeMsg:
        m.width, m.height = msg.Width, msg.Height
    case MsgPeerFound:
        m.peers = appendOrUpdate(m.peers, msg.Info)
    case MsgPeerLost:
        m.peers = removePeer(m.peers, msg.ID)
    case MsgTextReceived:
        m.messages = append(m.messages, Message{From: msg.From, Content: msg.Content})
    case MsgFileStart:
        m.fileXfer = &fileXfer{From: msg.From, Name: msg.Name, Total: msg.Size}
    case MsgFileProgress:
        if m.fileXfer != nil {
            m.fileXfer.Received = msg.Received
        }
    case MsgFileDone:
        if m.fileXfer != nil {
            m.fileXfer.Done = true
            m.fileXfer.SavePath = msg.SavePath
        }
    case MsgError:
        m.err = msg.Err
    }
    return m, nil
}

func (m Model) View() string {
    switch m.screen {
    case ScreenSetup:
        return setupView(m)
    default:
        return mainView(m)
    }
}

func appendOrUpdate(peers []bnet.PeerInfo, info bnet.PeerInfo) []bnet.PeerInfo {
    for i, p := range peers {
        if samePeer(p, info) {
            peers[i] = info
            return peers
        }
    }
    return append(peers, info)
}

func removePeer(peers []bnet.PeerInfo, id peer.ID) []bnet.PeerInfo {
    out := peers[:0]
    for _, p := range peers {
        if p.ID != id {
            out = append(out, p)
        }
    }
    return out
}

func samePeer(a, b bnet.PeerInfo) bool {
    if a.ID != "" && b.ID != "" {
        return a.ID == b.ID
    }
    if len(a.Addrs) > 0 && len(b.Addrs) > 0 {
        return a.Addrs[0].String() == b.Addrs[0].String()
    }
    return false
}
```

- [ ] **Step 3: Verify build**

```bash
go build ./...
```

Expected: no errors

- [ ] **Step 4: Commit**

```bash
git add internal/ui/styles.go internal/ui/app.go
git commit -m "feat: Bubble Tea root model and Nord color styles"
```

---

## Task 9: TUI Setup Screen

**Files:**
- Create: `internal/ui/setup.go`

- [ ] **Step 1: Implement setup screen**

Create `internal/ui/setup.go`:

```go
package ui

import (
    "strings"

    tea "github.com/charmbracelet/bubbletea"
    "github.com/charmbracelet/lipgloss"
)

type MsgNicknameSet struct{ Nickname string }

func setupView(m Model) string {
    center := lipgloss.NewStyle().
        Width(m.width).
        Align(lipgloss.Center)

    box := lipgloss.NewStyle().
        Border(lipgloss.RoundedBorder()).
        BorderForeground(colorPrimary).
        Padding(1, 3).
        Width(40)

    content := strings.Join([]string{
        StyleTitle.Render("bdpeer"),
        "",
        StyleHelp.Render("P2P file & message sharing"),
        "",
        "Enter your nickname:",
        "",
        "> " + m.inputBuf + "█",
        "",
        StyleHelp.Render("Press Enter to confirm"),
    }, "\n")

    return lipgloss.Place(m.width, m.height,
        lipgloss.Center, lipgloss.Center,
        box.Render(content),
    )
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
    if m.screen == ScreenSetup {
        return m.handleSetupKey(msg)
    }
    return m.handleMainKey(msg)
}

func (m Model) handleSetupKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
    switch msg.Type {
    case tea.KeyEnter:
        nick := strings.TrimSpace(m.inputBuf)
        if nick == "" {
            return m, nil
        }
        m.nickname = nick
        m.inputBuf = ""
        m.screen = ScreenMain
        return m, func() tea.Msg { return MsgNicknameSet{Nickname: nick} }
    case tea.KeyBackspace, tea.KeyDelete:
        if len(m.inputBuf) > 0 {
            m.inputBuf = m.inputBuf[:len(m.inputBuf)-1]
        }
    case tea.KeyCtrlC, tea.KeyEsc:
        return m, tea.Quit
    default:
        if msg.Type == tea.KeyRunes {
            m.inputBuf += string(msg.Runes)
        }
    }
    return m, nil
}
```

- [ ] **Step 2: Add placeholder handleMainKey to app.go**

Add to `internal/ui/app.go`:

```go
func (m Model) handleMainKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
    switch msg.Type {
    case tea.KeyCtrlC:
        return m, tea.Quit
    }
    return m, nil
}
```

- [ ] **Step 3: Verify build**

```bash
go build ./...
```

Expected: no errors

- [ ] **Step 4: Commit**

```bash
git add internal/ui/setup.go internal/ui/app.go
git commit -m "feat: nickname setup screen with centered layout"
```

---

## Task 10: TUI Main View (Peer List + Chat)

**Files:**
- Create: `internal/ui/peerlist.go`
- Create: `internal/ui/chat.go`
- Modify: `internal/ui/app.go`

- [ ] **Step 1: Implement peer list view**

Create `internal/ui/peerlist.go`:

```go
package ui

import (
    "fmt"
    "strings"

    bnet "github.com/chad/bdpeer/internal/net"
)

func peerListView(m Model, width, height int) string {
    var sb strings.Builder
    sb.WriteString(StyleTitle.Render("Peers") + "\n\n")

    if len(m.peers) == 0 {
        sb.WriteString(StyleHelp.Render("searching...") + "\n")
    }
    for i, p := range m.peers {
        name := p.Nickname
        if name == "" && p.ID != "" {
            name = p.ID.String()[:8]
        } else if name == "" {
            name = p.Source
        }
        cursor := "  "
        if m.activePeer != nil && m.activePeer.ID == p.ID {
            cursor = "▶ "
        }
        style := StylePeerOnline
        line := fmt.Sprintf("%s%s", cursor, style.Render(name))
        _ = i
        sb.WriteString(line + "\n")
    }

    sb.WriteString("\n" + StyleHelp.Render("↑/↓ select  a add IP  q quit"))
    return StylePanel.Width(width).Height(height).Render(sb.String())
}
```

- [ ] **Step 2: Implement chat view**

Create `internal/ui/chat.go`:

```go
package ui

import (
    "fmt"
    "strings"
)

func chatView(m Model, width, height int) string {
    if m.activePeer == nil {
        empty := StyleHelp.Render("Select a peer to start chatting")
        return StylePanel.Width(width).Height(height).Render(empty)
    }

    header := StyleTitle.Render("Chat: " + m.activePeer.Nickname)

    msgHeight := height - 6
    var msgs []string
    start := 0
    if len(m.messages) > msgHeight {
        start = len(m.messages) - msgHeight
    }
    for _, msg := range m.messages[start:] {
        var line string
        if msg.Mine {
            line = StyleMessageMine.Render("you: ") + msg.Content
        } else {
            line = StyleMessage.Render(msg.From+": ") + msg.Content
        }
        msgs = append(msgs, line)
    }

    msgArea := strings.Join(msgs, "\n")

    input := StyleInput.Width(width - 4).Render("> " + m.inputBuf)

    var xfer string
    if m.fileXfer != nil && !m.fileXfer.Done {
        pct := 0
        if m.fileXfer.Total > 0 {
            pct = int(float64(m.fileXfer.Received) / float64(m.fileXfer.Total) * 100)
        }
        xfer = StyleHelp.Render(fmt.Sprintf("Receiving %s... %d%%", m.fileXfer.Name, pct)) + "\n"
    } else if m.fileXfer != nil && m.fileXfer.Done {
        xfer = StyleHelp.Render(fmt.Sprintf("✓ %s saved to %s", m.fileXfer.Name, m.fileXfer.SavePath)) + "\n"
    }

    help := StyleHelp.Render("Enter send  /file <path> send file  Esc cancel")

    body := header + "\n" + msgArea + "\n" + xfer + input + "\n" + help
    return StylePanel.Width(width).Height(height).Render(body)
}
```

- [ ] **Step 3: Add mainView to app.go**

Add to `internal/ui/app.go`:

```go
import "github.com/charmbracelet/lipgloss"

func mainView(m Model) string {
    if m.width == 0 {
        return "loading..."
    }
    leftW := 22
    rightW := m.width - leftW - 4

    left := peerListView(m, leftW, m.height-2)
    right := chatView(m, rightW, m.height-2)

    title := lipgloss.NewStyle().
        Width(m.width).
        Foreground(colorPrimary).
        Bold(true).
        Render(fmt.Sprintf(" bdpeer  [%s]", m.nickname))

    row := lipgloss.JoinHorizontal(lipgloss.Top, left, right)
    return title + "\n" + row
}
```

Also add `"fmt"` to imports in app.go.

- [ ] **Step 4: Add peer navigation to handleMainKey**

Replace `handleMainKey` in `internal/ui/app.go`:

```go
func (m Model) handleMainKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
    switch msg.Type {
    case tea.KeyCtrlC:
        return m, tea.Quit
    case tea.KeyUp:
        m.activePeer = prevPeer(m.peers, m.activePeer)
    case tea.KeyDown:
        m.activePeer = nextPeer(m.peers, m.activePeer)
    case tea.KeyEnter:
        content := strings.TrimSpace(m.inputBuf)
        if content != "" && m.activePeer != nil && m.activePeer.ID != "" {
            m.messages = append(m.messages, Message{From: m.nickname, Content: content, Mine: true})
            m.inputBuf = ""
            return m, func() tea.Msg {
                return sendText{To: m.activePeer.ID, Content: content}
            }
        }
    case tea.KeyBackspace:
        if len(m.inputBuf) > 0 {
            m.inputBuf = m.inputBuf[:len(m.inputBuf)-1]
        }
    case tea.KeyRunes:
        m.inputBuf += string(msg.Runes)
    }
    return m, nil
}

type sendText struct {
    To      peer.ID
    Content string
}

func prevPeer(peers []bnet.PeerInfo, active *bnet.PeerInfo) *bnet.PeerInfo {
    if len(peers) == 0 {
        return nil
    }
    if active == nil {
        p := peers[len(peers)-1]
        return &p
    }
    for i, p := range peers {
        if p.ID == active.ID && i > 0 {
            prev := peers[i-1]
            return &prev
        }
    }
    return active
}

func nextPeer(peers []bnet.PeerInfo, active *bnet.PeerInfo) *bnet.PeerInfo {
    if len(peers) == 0 {
        return nil
    }
    if active == nil {
        p := peers[0]
        return &p
    }
    for i, p := range peers {
        if p.ID == active.ID && i < len(peers)-1 {
            next := peers[i+1]
            return &next
        }
    }
    return active
}
```

Also add import `"strings"` to app.go.

- [ ] **Step 5: Verify build**

```bash
go build ./...
```

Expected: no errors

- [ ] **Step 6: Commit**

```bash
git add internal/ui/
git commit -m "feat: peer list and chat TUI panels"
```

---

## Task 11: Wire Everything in main.go

**Files:**
- Modify: `cmd/bdpeer/main.go`

- [ ] **Step 1: Implement main.go as a thin core adapter**

Use one final `main.go` implementation. Do not add an intermediate version with host/discovery setup in `cmd/bdpeer`; `internal/core.Service` owns that. The command only loads config, creates the service, starts the TUI, forwards core events into Bubble Tea messages, and sends user actions back to `svc.Send`.

```go
package main

import (
    "context"
    "fmt"
    "os"

    tea "github.com/charmbracelet/bubbletea"
    "github.com/chad/bdpeer/internal/config"
    "github.com/chad/bdpeer/internal/core"
    "github.com/chad/bdpeer/internal/ui"
)

func main() {
    cfgPath := config.DefaultPath()
    cfg, err := config.Load(cfgPath)
    if err != nil {
        fmt.Fprintln(os.Stderr, "config:", err)
        os.Exit(1)
    }

    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()

    svc := core.NewService(cfg, cfgPath)
    sendCh := make(chan core.SendRequest, 16)
    nickCh := make(chan string, 1)
    if cfg.Nickname != "" {
        nickCh <- cfg.Nickname
    }

    model := ui.NewWithChannels(cfg.Nickname, sendCh, nickCh)
    prog := tea.NewProgram(model, tea.WithAltScreen())

    go startCoreAfterNickname(ctx, svc, nickCh, sendCh, prog, cfg, cfgPath)
    go forwardCoreEvents(svc.Events(), prog)

    if _, err := prog.Run(); err != nil {
        fmt.Fprintln(os.Stderr, err)
        os.Exit(1)
    }
    cancel()
    _ = svc.Stop()
}

func startCoreAfterNickname(
    ctx context.Context,
    svc *core.Service,
    nickCh <-chan string,
    sendCh <-chan core.SendRequest,
    prog *tea.Program,
    cfg *config.Config,
    cfgPath string,
) {
    var nickname string
    select {
    case nickname = <-nickCh:
    case <-ctx.Done():
        return
    }
    cfg.Nickname = nickname
    _ = cfg.Save(cfgPath)

    go func() {
        if err := svc.Start(ctx); err != nil {
            prog.Send(ui.MsgError{Err: err})
        }
    }()

    for {
        select {
        case req := <-sendCh:
            go func(r core.SendRequest) {
                if err := svc.Send(ctx, r); err != nil {
                    prog.Send(ui.MsgError{Err: err})
                }
            }(req)
        case <-ctx.Done():
            return
        }
    }
}

func forwardCoreEvents(events <-chan core.Event, prog *tea.Program) {
    for ev := range events {
        switch ev.Type {
        case core.EventPeerFound:
            prog.Send(ui.MsgPeerFound{Info: ev.Peer})
        case core.EventPeerLost:
            prog.Send(ui.MsgPeerLost{ID: ev.Peer.ID})
        case core.EventTextReceived:
            prog.Send(ui.MsgTextReceived{From: ev.From, Content: ev.Content})
        case core.EventFileStart:
            prog.Send(ui.MsgFileStart{From: ev.From, Name: ev.Name, Size: ev.Size})
        case core.EventFileProgress:
            prog.Send(ui.MsgFileProgress{From: ev.From, Received: ev.Received, Total: ev.Total})
        case core.EventFileDone:
            prog.Send(ui.MsgFileDone{From: ev.From, Name: ev.Name, SavePath: ev.Path})
        case core.EventError:
            prog.Send(ui.MsgError{Err: ev.Err})
        }
    }
}
```

- [ ] **Step 2: Add NewWithChannels to ui/app.go**

Add to `internal/ui/app.go`:

```go
import "github.com/chad/bdpeer/internal/core"

type Model struct {
    // existing fields...
    sendCh chan<- core.SendRequest  // add
    nickCh chan<- string            // add
}

func NewWithChannels(nickname string, sendCh chan<- core.SendRequest, nickCh chan<- string) Model {
    m := New(nickname)
    m.sendCh = sendCh
    m.nickCh = nickCh
    return m
}
```

Update `handleSetupKey` Enter case to also send to nickCh:

```go
case tea.KeyEnter:
    nick := strings.TrimSpace(m.inputBuf)
    if nick == "" {
        return m, nil
    }
    m.nickname = nick
    m.inputBuf = ""
    m.screen = ScreenMain
    if m.nickCh != nil {
        ch := m.nickCh
        return m, func() tea.Msg {
            go func() { ch <- nick }()
            return nil
        }
    }
    return m, nil
```

Update `handleMainKey` Enter case to send via core request:

```go
case tea.KeyEnter:
    content := strings.TrimSpace(m.inputBuf)
    if content != "" && m.activePeer != nil && m.activePeer.ID != "" {
        m.messages = append(m.messages, Message{From: m.nickname, Content: content, Mine: true})
        if m.sendCh != nil {
            select {
            case m.sendCh <- core.SendRequest{To: m.activePeer.ID, Content: content}:
            default:
            }
        }
        m.inputBuf = ""
    }
```

- [ ] **Step 3: Build and run**

```bash
go build ./... && go build -o dist/bdpeer ./cmd/bdpeer
./dist/bdpeer
```

Expected: TUI launches, setup screen shown if no config, main screen shown with peer list

- [ ] **Step 4: Commit**

```bash
git add cmd/ internal/ui/app.go internal/core/
git commit -m "feat: wire thin TUI adapter to bdpeer-core service"
```

---

## Task 12: /file Command in Chat

**Files:**
- Modify: `internal/ui/app.go`
- Modify: `internal/core/service.go` (file send/receive handling)

- [ ] **Step 1: Parse /file command in handleMainKey**

In `internal/ui/app.go` `handleMainKey` Enter case, before sending text check for `/file`:

```go
case tea.KeyEnter:
    content := strings.TrimSpace(m.inputBuf)
    if content == "" || m.activePeer == nil || m.activePeer.ID == "" {
        return m, nil
    }
    if strings.HasPrefix(content, "/file ") {
        path := strings.TrimPrefix(content, "/file ")
        path = strings.TrimSpace(path)
        if m.sendCh != nil {
            select {
            case m.sendCh <- core.SendRequest{To: m.activePeer.ID, File: path}:
            default:
            }
        }
    } else {
        m.messages = append(m.messages, Message{From: m.nickname, Content: content, Mine: true})
        if m.sendCh != nil {
            select {
            case m.sendCh <- core.SendRequest{To: m.activePeer.ID, Content: content}:
            default:
            }
        }
    }
    m.inputBuf = ""
```

- [ ] **Step 2: Handle file send in core.Service.Send**

In `internal/core/service.go`, update `Service.Send` so file/text sending stays in bdpeer-core:

```go
func (s *Service) Send(ctx context.Context, req SendRequest) error {
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

Add `"io"`, `"github.com/chad/bdpeer/internal/proto"`, and `"github.com/chad/bdpeer/internal/transfer"` to `internal/core/service.go` imports. `Service` needs a `host *bnet.Host` field set during `Start`.

- [ ] **Step 3: Handle incoming file in core.Service.Start**

Inside `Service.Start`, set frame callbacks on the host and emit core events. `cmd/bdpeer` should only forward these events to UI messages:

```go
s.host.OnFrame = func(_ peer.ID, frame proto.Frame) {
    switch frame.Type {
    case proto.FrameText:
        s.events <- Event{Type: EventTextReceived, From: frame.From, Content: frame.Content}
    case proto.FrameFileStart:
        s.events <- Event{Type: EventFileStart, From: frame.From, Name: frame.Name, Size: frame.Size}
    }
}
```

> Note: The current stream handler reads one frame at a time. File transfer requires reading a sequence: START → CHUNK... → END. Update `internal/net/stream.go`'s stream handler to detect `FrameFileStart` and then delegate to `transfer.ReadFile`.

Update `internal/net/stream.go`:

```go
func (h *Host) StartStreamHandler() {
    h.Libp2p.SetStreamHandler(proto.Protocol, func(s network.Stream) {
        defer s.Close()
        r := bufio.NewReader(s)
        for {
            frame, err := transfer.ReadFrame(r)
            if err != nil {
                return
            }
            if frame.Type == proto.FrameFileStart {
                // Delegate full file read to OnFileStream
                if h.OnFileStream != nil {
                    h.OnFileStream(s.Conn().RemotePeer(), frame, r)
                }
                return // stream consumed by file handler
            }
            if h.OnFrame != nil {
                h.OnFrame(s.Conn().RemotePeer(), frame)
            }
        }
    })
}
```

Add `OnFileStream func(peer.ID, proto.Frame, io.Reader)` to `Host` struct in host.go.

In `Service.Start`, add after `s.host.OnFrame = ...`:

```go
s.host.OnFileStream = func(_ peer.ID, startFrame proto.Frame, r io.Reader) {
    s.events <- Event{Type: EventFileStart, From: startFrame.From, Name: startFrame.Name, Size: startFrame.Size}
    
    // Reconstruct full reader: startFrame already consumed, need to pass remaining stream
    // We re-encode startFrame back and prepend to a MultiReader
    var startBuf bytes.Buffer
    transfer.WriteFrame(&startBuf, startFrame)
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
```

Add `"bytes"` import and a `recvDir string` field to `Service`.

- [ ] **Step 4: Build**

```bash
go build ./...
```

Expected: no errors

- [ ] **Step 5: Commit**

```bash
git add internal/core/ internal/net/ internal/ui/
git commit -m "feat: handle file transfer through bdpeer-core"
```

---

## Task 12b: Final `Service.Start` Wiring (consolidated)

`Service.Start` is built up across Tasks 7b, 4f, and 12. Use this section as the single source of truth for the final shape. If your in-progress `service.go` diverges from this, reconcile here before moving on.

```go
package core

import (
    "bytes"
    "context"
    "fmt"
    "io"
    "path/filepath"

    "github.com/chad/bdpeer/internal/config"
    "github.com/chad/bdpeer/internal/discovery"
    bnet "github.com/chad/bdpeer/internal/net"
    "github.com/chad/bdpeer/internal/proto"
    "github.com/chad/bdpeer/internal/transfer"
    "github.com/libp2p/go-libp2p/core/peer"
    "github.com/multiformats/go-multiaddr"
    maddr "github.com/multiformats/go-multiaddr"
)

func (s *Service) Start(ctx context.Context) error {
    // 1. Persisted identity
    if s.cfg.PrivateKeyB64 == "" {
        encoded, err := bnet.GenerateIdentityB64()
        if err != nil {
            return fmt.Errorf("generate identity: %w", err)
        }
        s.cfg.PrivateKeyB64 = encoded
        _ = s.cfg.Save(s.cfgPath)
    }
    priv, err := bnet.PrivateKeyFromB64(s.cfg.PrivateKeyB64)
    if err != nil {
        return fmt.Errorf("load identity: %w", err)
    }

    // 2. libp2p host
    s.host, err = bnet.NewHostWithIdentity(ctx, s.cfg.Nickname, priv)
    if err != nil {
        return fmt.Errorf("new host: %w", err)
    }

    // 3. Receive directory
    if s.recvDir == "" {
        s.recvDir = filepath.Join(s.cfg.DataDir, "received")
    }

    // 4. Frame and file callbacks (must be set before StartStreamHandler)
    s.host.OnFrame = func(_ peer.ID, frame proto.Frame) {
        switch frame.Type {
        case proto.FrameText:
            s.events <- Event{Type: EventTextReceived, From: frame.From, Content: frame.Content}
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

    // 5. Stream handler — must come after OnFrame/OnFileStream are set
    s.host.StartStreamHandler()

    // 6. Discovery manager
    mgr := discovery.NewManager(s.cfg.Nickname)
    mgr.OnPeerFound = func(p discovery.DiscoveredPeer) {
        if p.ID != "" && p.Nickname != "" {
            s.host.RememberNickname(p.ID, p.Nickname)
        }
        s.events <- Event{Type: EventPeerFound, Peer: bnet.PeerInfo{
            ID: p.ID, Nickname: p.Nickname, Addrs: p.Addrs, Source: p.Source,
        }}
    }
    mgr.OnPeerLost = func(p discovery.DiscoveredPeer) {
        s.events <- Event{Type: EventPeerLost, Peer: bnet.PeerInfo{ID: p.ID}}
    }
    s.host.AttachDiscovery(mgr)

    // 7. Discovery backends
    if stop, err := discovery.BrowseBonjour(ctx, mgr); err == nil {
        s.stops = append(s.stops, stop)
        if server, err := discovery.RegisterBonjour(s.cfg.Nickname, listenPort(s.host), hostFullAddrs(s.host)); err == nil {
            s.stops = append(s.stops, func() { server.Shutdown() })
        }
    }
    if stop, err := discovery.SearchSSDP(ctx, mgr); err == nil {
        s.stops = append(s.stops, stop)
        if advStop, err := discovery.AdvertiseSSDP(ctx, s.cfg.Nickname, listenPort(s.host)); err == nil {
            s.stops = append(s.stops, advStop)
        }
    }
    _ = discovery.ListenWSD(ctx, mgr)                                   // best-effort
    _ = discovery.SendWSDHello(ctx, s.cfg.Nickname, listenPort(s.host)) // best-effort
    go discovery.StartBLE(ctx, s.cfg.Nickname, mgr)                     // no-op without -tags ble

    // 8. Block until ctx is cancelled, then tear down
    <-ctx.Done()
    return s.Stop()
}

func listenPort(h *bnet.Host) int {
    for _, a := range h.Libp2p.Addrs() {
        if portStr, err := a.ValueForProtocol(maddr.P_TCP); err == nil {
            var port int
            fmt.Sscanf(portStr, "%d", &port)
            return port
        }
    }
    return 4001
}

func hostFullAddrs(h *bnet.Host) []multiaddr.Multiaddr {
    out := make([]multiaddr.Multiaddr, 0, len(h.Libp2p.Addrs()))
    suffix, _ := multiaddr.NewMultiaddr("/p2p/" + h.Libp2p.ID().String())
    for _, a := range h.Libp2p.Addrs() {
        if _, err := a.ValueForProtocol(maddr.P_TCP); err == nil {
            out = append(out, a.Encapsulate(suffix))
        }
    }
    return out
}
```

- [ ] **Step 1: Reconcile your `service.go` with the block above**

If you followed Tasks 7b → 4f → 12 in order, your file should already match. If anything diverges (missing `StartStreamHandler`, ad-hoc `defer` in `Start`, channel buffer size, etc.), update to match this block.

- [ ] **Step 2: Verify**

```bash
go build ./...
go test ./internal/core/... ./internal/net/... ./internal/discovery/... ./internal/transfer/... -timeout 30s
```

- [ ] **Step 3: Commit**

```bash
git add internal/core/service.go
git commit -m "refactor: consolidate Service.Start into a single ordered flow"
```

---

## Task 13: Cross-Platform Build Verification

**Files:**
- Modify: `Makefile`
- Create: `README.md` (minimal usage instructions only)

- [ ] **Step 1: Build all targets**

```bash
make build-mac build-win build-linux
ls -lh dist/
```

Expected: four binaries (mac-amd64, mac-arm64, win-amd64.exe, linux-amd64), each under 30 MB

- [ ] **Step 2: Smoke test on current platform**

```bash
./dist/bdpeer
```

Expected: TUI launches, setup screen shown

- [ ] **Step 3: Run all tests**

```bash
go test ./... -timeout 30s
```

Expected: all PASS

- [ ] **Step 4: Final commit**

```bash
printf "dist/\n" >> .gitignore
git add Makefile README.md .gitignore
git commit -m "chore: add cross-platform build targets and ignore artifacts"
```

---

## Spec Coverage Check

| Requirement | Task | Status |
|---|---|---|
| Mac/Windows/Linux | Task 13 (Makefile cross-compile) | ✅ |
| Bonjour (macOS/iOS/Android/Win10+) | Task 4b (`grandcat/zeroconf` `_bdpeer._tcp`) | ✅ |
| Samba/Windows LAN (WSD) | Task 4d (WS-Discovery UDP 3702) | ✅ |
| SSDP/UPnP | Task 4c (`koron/go-ssdp`) | ✅ |
| Bluetooth (BLE proximity) | Task 4e (`tinygo-org/bluetooth`, `-tags ble`) | ✅ |
| AirDrop | **Not fully implementable** — AWDL is Apple proprietary. Visible via Bonjour on same WiFi | ⚠️ |
| Quick Share (Android) | **Not fully implementable** — Nearby Connections wire protocol required. Phase 3 | ⚠️ |
| Internet P2P | Task 3 starts libp2p + DHT only; relay bootstrap/reservation remains a later task | ⚠️ |
| Nickname | Task 2 (config) + Task 9 (TUI setup) | ✅ |
| Find peer by nickname | Tasks 4b/4c/4d (all backends broadcast nickname) | ✅ |
| Find peer by IP | Task 7 (ConnectByAddr multiaddr) | ✅ |
| Text message | Task 5 + Task 11 | ✅ |
| File transfer | Task 6 + Task 12 | ✅ |

### AirDrop 상세 설명
AirDrop은 **AWDL (Apple Wireless Direct Link)** 을 사용합니다. AWDL은 Apple의 독점 프로토콜로 표준 WiFi Direct가 아닙니다. 구현 불가 이유:
- macOS/iOS private API (`AWDLBrowseActivity`, `AWDLAdvertiseActivity`)
- Apple 개발자 entitlement 필요
- 리버스 엔지니어링 시 ToS 위반

대신 **같은 WiFi에서 Bonjour(`_bdpeer._tcp`)로 iOS/macOS에서 발견 가능**합니다.

### Quick Share (Android/Windows) 상세 설명
Google Nearby Connections 프로토콜: BLE advertisement → mDNS → WiFi Direct TCP 핸드셰이크. 공식 SDK 없이 구현하려면 wire protocol을 직접 구현해야 합니다 (Phase 3).

### BLE 빌드 방법
```bash
# BLE 포함 빌드 (macOS)
go build -tags ble -o dist/bdpeer-ble ./cmd/bdpeer

# 기본 빌드 (BLE 없음, CGo 불필요)
go build -o dist/bdpeer ./cmd/bdpeer
```
