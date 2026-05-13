# bdpeer TUI — Implementation Task Breakdown

> Companion to [2026-05-13-bdpeer-tui.md](2026-05-13-bdpeer-tui.md). 이 파일은 실행 순서 체크리스트이고, 세부 코드 예시·시그니처는 plan 파일의 Task 1~13 / 12b를 참고한다.

이 섹션이 최신 스펙 기준의 실행 순서다. 각 항목의 Scope/Deliverable/Verify를 만족하면 체크.

## Phase 1: bdpeer-core foundation

- [ ] **Core Task 1: Go module and protocol scaffold**
  - Scope: `go.mod`, `Makefile`, `cmd/bdpeer/main.go`, `internal/proto/protocol.go`, `internal/net/host.go`
  - Deliverable: `/bdpeer/1.0.0` frame type, `PeerInfo`, basic build target
  - Verify: `go build ./...`
  - Detail: plan §Task 1

- [ ] **Core Task 2: Config and stable identity storage**
  - Scope: `internal/config`, `internal/net` identity helpers
  - Deliverable: nickname, data dir, `PrivateKeyB64`, private-key round trip helpers
  - Verify: `go test ./internal/config/... ./internal/net/... -run 'TestSaveAndLoad|TestPrivateKeyRoundTrip|TestNewHost' -v`
  - Detail: plan §Task 2, §Task 3b

- [ ] **Core Task 3: libp2p host and direct connect**
  - Scope: `internal/net/host.go`, `internal/net/connect.go`
  - Deliverable: TCP/QUIC host, DHT bootstrap, `ConnectByAddr`, `FirstTCPAddr`, `AttachDiscovery`
  - Verify: `go test ./internal/net/... -run 'TestNewHost|TestConnectByMultiaddr' -v -timeout 15s`
  - Detail: plan §Task 3, §Task 7

- [ ] **Core Task 4: frame codec and transfer primitives**
  - Scope: `internal/proto/protocol.go`, `internal/transfer/message.go`, `internal/transfer/file.go`, `internal/net/stream.go`
  - Deliverable: length-prefixed JSON frames, text frames, chunked file transfer, checksum validation
  - Verify: `go test ./internal/transfer/... ./internal/net/... -timeout 30s`
  - Detail: plan §Task 5, §Task 6

- [ ] **Core Task 5: discovery backends**
  - Scope: `internal/discovery`
  - Deliverable: manager dedupe, Bonjour, SSDP, WSD, BLE stub/default, optional BLE build-tag files
  - Verify: `go test ./internal/discovery/... -timeout 30s`; BLE separately with `go build -tags ble ./...`
  - Detail: plan §Task 4, §Task 4b, §Task 4c, §Task 4d, §Task 4e

- [ ] **Core Task 6: bdpeer-core service facade**
  - Scope: `internal/core/service.go`
  - Deliverable: `Service`, `Event`, `SendRequest`, `Start`, `Events`, `Send`
  - Verify: `go test ./internal/core/... ./internal/net/... ./internal/discovery/... ./internal/transfer/... -timeout 30s`
  - Detail: plan §Task 7b

- [ ] **Core Task 7: wire core runtime**
  - Scope: `internal/core/service.go`
  - Deliverable: `Service.Start` creates persisted identity host, attaches discovery, starts stream handlers, emits peer/text/file events; `Service.Send` sends text/files
  - Verify: run two local processes with direct multiaddr and send a text frame; automated coverage can start with package tests if interactive smoke is not available
  - Detail: plan §Task 4f

## Phase 2: thin TUI adapter

- [ ] **TUI Task 1: Bubble Tea model and views**
  - Scope: `internal/ui`
  - Deliverable: setup screen, peer list, chat panel, progress state, no direct libp2p/discovery imports except shared types and `core.SendRequest`
  - Verify: `go test ./internal/ui/...` if tests exist, otherwise `go build ./...`
  - Detail: plan §Task 8, §Task 9, §Task 10

- [ ] **TUI Task 2: command wiring**
  - Scope: `cmd/bdpeer/main.go`, `internal/ui/app.go`
  - Deliverable: `cmd/bdpeer` loads config, starts `core.Service`, forwards `core.Event` to UI messages, sends UI actions to `core.Service.Send`
  - Verify: `go build -o dist/bdpeer ./cmd/bdpeer`
  - Detail: plan §Task 11

- [ ] **TUI Task 3: `/file` command**
  - Scope: `internal/ui/app.go`, `internal/core/service.go`
  - Deliverable: UI parses `/file <path>` into `core.SendRequest{File: path}`; core handles send/receive and emits progress events
  - Verify: `go test ./internal/transfer/... ./internal/core/... -timeout 30s`
  - Detail: plan §Task 12

- [ ] **TUI Task 4: consolidate `Service.Start`**
  - Scope: `internal/core/service.go`
  - Deliverable: a single ordered `Service.Start` matching plan §Task 12b (identity → host → callbacks → `StartStreamHandler` → discovery → `<-ctx.Done()` → `Stop`)
  - Verify: `go build ./... && go test ./internal/core/... -timeout 30s`
  - Detail: plan §Task 12b

## Phase 3: packaging and platform checks

- [ ] **Packaging Task 1: cross-platform builds**
  - Scope: `Makefile`, `.gitignore`, `README.md`
  - Deliverable: macOS, Windows, Linux build targets; `dist/` ignored
  - Verify: `make build-mac build-win build-linux`
  - Detail: plan §Task 13

- [ ] **Packaging Task 2: smoke test**
  - Scope: built binary
  - Deliverable: TUI launches, nickname persists, app can advertise and show local peers where multicast is available
  - Verify: `./dist/bdpeer`; run two local instances for direct multiaddr text transfer if possible
  - Detail: plan §Task 13

- [ ] **Packaging Task 3: deferred internet relay note**
  - Scope: README/spec note
  - Deliverable: DHT is marked as initial internet discovery only; circuit relay bootstrap/reservation remains a future task
  - Verify: plan §Spec Coverage Check keeps Internet P2P as ⚠️ until relay work exists
