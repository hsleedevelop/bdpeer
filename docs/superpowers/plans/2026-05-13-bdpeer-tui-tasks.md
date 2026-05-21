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

## Phase 4: Windows BLE GATT stabilization

- [x] **BLE Task 1: README and release note update for v0.5.11**
  - Scope: `README.md`, release/tag notes
  - Deliverable: Windows support text reflects GATT direct text transfer, `BD9E0005` session char, session-addressed `S` chunks, and the fact that both Windows peers need v0.5.11+ for BLE direct messaging
  - Verify: README no longer describes Windows BLE as discovery-only; build comments say Windows BLE GATT, not only advertising+scanning

- [ ] **BLE Task 2: Windows two-peer smoke test**
  - Scope: built Windows binaries on two machines
  - Deliverable: Windows A and B discover each other with `id=ble-<session>` and send chat messages without `/connect` on different subnets
  - Verify: no duplicate message display; no self-echo; no repeated `id 미확정`; debug log shows `peer_found` with non-empty BLE synthetic ID
  - Watch: dual BLE connections can exist at the same time. Confirm A→B does not appear again on A through the opposite direction notification/write path.
  - Watch: `suppressNextWindowsLocalWrite` depends on callback ordering in tinygo/WinRT. Confirm real hardware does not reorder local write suppression and remote notification delivery.

- [ ] **BLE Task 3: Windows three-peer session-routing smoke test**
  - Scope: built Windows binaries on three nearby machines
  - Deliverable: A can send to B while C is also connected/subscribed, and C does not display B-targeted messages
  - Verify: session filter drops chunks whose `toSession` does not match local session; no chunk reassembly contamination between B and C
  - Watch: earlier `windowsDefaultPeer`/`currentWindowsDefaultPeer` single-peer routing was removed in favor of session IDs. Confirm logs and UI never fall back to a generic `ble-peripheral` or `peripheral` peer key.

- [ ] **BLE Task 4: Legacy and cross-platform fallback**
  - Scope: `internal/discovery/ble_windows.go`, `internal/discovery/ble_linux.go`, macOS BLE bridge if needed
  - Deliverable: Windows detects whether remote peer has `BD9E0005`; if absent, it uses legacy `D` chunks where still supported, or reports a clear unsupported-version log
  - Verify: Windows v0.5.11+ ↔ old Windows does not silently fail; Windows ↔ Linux/macOS behavior is documented and either works through legacy chunks or has a clear fallback path

- [ ] **BLE Task 5: Linux service UUID discovery fallback**
  - Scope: `internal/discovery/ble_linux.go`
  - Deliverable: Linux does not require manufacturer-data nickname before attempting bdpeer GATT service discovery; it can scan by `bleServiceUUID`, connect, then read NickChar
  - Verify: Linux can discover a Windows peer that advertises only the GATT service provider path, with no manufacturer data nickname

- [ ] **BLE Task 6: WinRT peer-specific notify investigation**
  - Scope: Windows BLE implementation below `tinygo.org/x/bluetooth`, likely direct `winrt-go`
  - Deliverable: determine whether Windows can map subscribed centrals and notify one peer only, replacing broadcast+session-filter routing if practical
  - Verify: proof-of-concept or documented decision; keep session-addressed chunks as fallback unless peer-specific notify is proven stable
