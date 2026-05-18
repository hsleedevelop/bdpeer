# BLE Data Transport (Phase 2) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a BLE GATT DataChar so peers can exchange bdpeer text frames over BLE — used as primary transport for macOS↔Linux/Windows, and as WebRTC fallback for macOS↔macOS.

**Architecture:** A new GATT characteristic (`BD9E0004`) is added to the existing bdpeer service alongside the SDP char. Both sides use the same 5-byte chunk protocol already in place for SDP, with type byte `'D'`. On macOS, a `BLETransport` (io.Pipe per peer + sendFn callback) plugs into the existing `transport.Registry`; when WebRTC succeeds it overwrites the BLE entry. On Linux/Windows, the existing scan-only flow is extended to connect as GATT central, subscribe to DataChar notifications, and write chunks outbound.

**Tech Stack:** CoreBluetooth (ObjC), tinygo/x/bluetooth (Linux/Windows central), Go io.Pipe, transport.Registry

---

## File Map

| File | Action | Responsibility |
|---|---|---|
| `internal/discovery/ble_corebluetooth.h` | Modify | Add DataChar C function declarations |
| `internal/discovery/ble_corebluetooth.m` | Modify | Add DataChar UUID, send/receive ObjC logic |
| `internal/discovery/ble_darwin.go` | Modify | Add data callback registration + CGo exports |
| `internal/discovery/ble_chunk.go` | Create | Platform-agnostic chunk make/assemble helpers |
| `internal/discovery/ble_linux.go` | Modify | GATT connect, DataChar subscribe/write |
| `internal/discovery/ble_windows.go` | Modify | Add data stubs |
| `internal/discovery/ble_stub.go` | Modify | Add data stubs |
| `internal/transport/ble.go` | Create | BLETransport implementing Transport interface |
| `internal/core/service.go` | Modify | Add `bleT *transport.BLETransport` field |
| `internal/core/service_ble.go` | Modify | Wire BLE data transport, WebRTC fallback |
| `internal/core/service_ble_stub.go` | Modify | Nil-safe stub for bleT |

---

## UUID / Chunk Protocol Reference

```
Service:  BD9E0001-F0F0-1000-8000-00805F9B34FB  (existing)
NickChar: BD9E0002-F0F0-1000-8000-00805F9B34FB  (existing, read)
SDPChar:  BD9E0003-F0F0-1000-8000-00805F9B34FB  (existing, write+notify)
DataChar: BD9E0004-F0F0-1000-8000-00805F9B34FB  (NEW, write+notify)

Chunk header (5 bytes):
  [type(1)] [idx_hi(1)] [idx_lo(1)] [total_hi(1)] [total_lo(1)]
  type = 'D' (0x44) for data frames
  max payload per chunk: 490 bytes (same as SDP)
```

## PeerID Scheme

- **Initiator** (lower nickname, acts as central): peerKey = `"ble-" + peripheralUUID` (from `go_ble_peer_found`). Sends via `BLECentralSendData(peripheralUUID, data)`.
- **Responder** (higher nickname, acts as peripheral): peerKey = `"ble-" + centralUUID` (from `go_ble_central_subscribed`). Sends via `BLEPeripheralSendDataTo(centralUUID, data)`.
- Nickname is resolved on first inbound Hello frame (same pattern as WebRTC Phase 1).
- **Linux/Windows**: macOS is identified by its peripheral UUID (from scan). macOS sees Linux as `centralUUID` from `go_ble_central_subscribed`.

---

## Task 1: ObjC DataChar infrastructure

**Files:**
- Modify: `internal/discovery/ble_corebluetooth.h`
- Modify: `internal/discovery/ble_corebluetooth.m`

- [ ] **Step 1: Add DataChar UUID constant and C function declarations to header**

```c
// ble_corebluetooth.h (add after existing declarations)

// DataChar characteristic UUID: BD9E0004-...
// Used for actual bdpeer frame data (type byte 'D').
void ble_peripheral_send_data_to(const char *central_uuid, const uint8_t *data, int len);
void ble_central_send_data(const char *peripheral_uuid, const uint8_t *data, int len);
```

- [ ] **Step 2: Add DataChar UUID and Go callback forward-declaration in .m**

In `ble_corebluetooth.m`, after existing UUID constants:
```objc
static NSString *const kDataCharUUIDStr = @"BD9E0004-F0F0-1000-8000-00805F9B34FB";
static CBUUID *dataUUID(void) { return [CBUUID UUIDWithString:kDataCharUUIDStr]; }

// Add this forward-declare alongside the other Go callback externs
extern void go_ble_data_received(const char *peer_uuid, const uint8_t *data, int len);
```

- [ ] **Step 3: Add DataChar property and data buffers to BDPeerBLE interface**

In the `@interface BDPeerBLE` block, after `_sdpChar`:
```objc
@property (nonatomic, strong) CBMutableCharacteristic  *dataChar;
@property (nonatomic, strong) NSMutableDictionary<NSString *, NSMutableData *> *centralDataBufs;
@property (nonatomic, strong) NSMutableDictionary<NSString *, NSMutableData *> *peripheralDataBufs;
```

In `initWithNickname:`, initialize the new dicts:
```objc
_centralDataBufs    = [NSMutableDictionary new];
_peripheralDataBufs = [NSMutableDictionary new];
```

- [ ] **Step 4: Add DataChar to peripheral service setup**

In `peripheralManagerDidUpdateState:`, after `_sdpChar` is created, add:
```objc
_dataChar = [[CBMutableCharacteristic alloc]
    initWithType:dataUUID()
      properties:CBCharacteristicPropertyWriteWithoutResponse | CBCharacteristicPropertyNotify
           value:nil
     permissions:CBAttributePermissionsWriteable];
```

Change `svc.characteristics` line to include `_dataChar`:
```objc
svc.characteristics = @[nickChar, _sdpChar, _dataChar];
```

- [ ] **Step 5: Route DataChar writes to data handler in peripheral delegate**

In `peripheralManager:didReceiveWriteRequests:`, add a branch for DataChar:
```objc
for (CBATTRequest *req in requests) {
    if ([req.characteristic.UUID isEqual:sdpUUID()]) {
        [self handleChunk:req.value
                 inBufMap:_centralSDPBufs
                      key:req.central.identifier.UUIDString
                 callback:^(NSString *key, NSData *data) {
                     go_ble_sdp_received([key UTF8String], [data bytes], (int)data.length, 1);
                 }];
    } else if ([req.characteristic.UUID isEqual:dataUUID()]) {
        [self handleChunk:req.value
                 inBufMap:_centralDataBufs
                      key:req.central.identifier.UUIDString
                 callback:^(NSString *key, NSData *data) {
                     go_ble_data_received([key UTF8String], (const uint8_t *)data.bytes, (int)data.length);
                 }];
    }
}
```

Note: `go_ble_sdp_received` currently takes `(char*, char*, int)`. We'll refactor `handleChunk` to use a block callback in Step 7.

- [ ] **Step 6: Subscribe to DataChar on central discovery**

In `peripheral:didDiscoverCharacteristicsForService:error:`, add DataChar subscription:
```objc
for (CBCharacteristic *c in s.characteristics) {
    if ([c.UUID isEqual:nickUUID()]) [p readValueForCharacteristic:c];
    if ([c.UUID isEqual:sdpUUID()])  [p setNotifyValue:YES forCharacteristic:c];
    if ([c.UUID isEqual:dataUUID()]) [p setNotifyValue:YES forCharacteristic:c];
}
```

Also update `discoverCharacteristics:` call to include `dataUUID()`:
```objc
[p discoverCharacteristics:@[nickUUID(), sdpUUID(), dataUUID()] forService:s];
```

- [ ] **Step 7: Route DataChar notifications to data handler in central delegate**

In `peripheral:didUpdateValueForCharacteristic:error:`, add DataChar branch:
```objc
if ([c.UUID isEqual:dataUUID()]) {
    [self handleDataChunk:c.value
                 inBufMap:_peripheralDataBufs
                      key:p.identifier.UUIDString];
}
```

Add a new method alongside `handleChunk`:
```objc
- (void)handleDataChunk:(NSData *)chunk
               inBufMap:(NSMutableDictionary<NSString *, NSMutableData *> *)bufs
                    key:(NSString *)key {
    if (chunk.length < CHUNK_HDR) return;
    const uint8_t *b = chunk.bytes;
    uint16_t idx   = ((uint16_t)b[1] << 8) | b[2];
    uint16_t total = ((uint16_t)b[3] << 8) | b[4];

    if (!bufs[key]) bufs[key] = [NSMutableData new];
    [bufs[key] appendBytes:b + CHUNK_HDR length:chunk.length - CHUNK_HDR];

    if (idx == total - 1) {
        NSData *assembled = bufs[key];
        bufs[key] = nil;
        go_ble_data_received([key UTF8String], (const uint8_t *)assembled.bytes, (int)assembled.length);
    }
}
```

- [ ] **Step 8: Add peripheral data send method and C API functions**

In `BDPeerBLE` implementation, add:
```objc
- (void)sendDataToCentral:(NSString *)centralUUID data:(NSData *)data {
    CBCentral *target = nil;
    for (CBCentral *c in _subscribedCentrals) {
        if ([c.identifier.UUIDString isEqualToString:centralUUID]) {
            target = c;
            break;
        }
    }
    if (!target) return;

    uint16_t total = (uint16_t)((data.length + CHUNK_BODY - 1) / CHUNK_BODY);
    for (uint16_t i = 0; i < total; i++) {
        NSUInteger offset = (NSUInteger)i * CHUNK_BODY;
        NSUInteger len    = MIN(CHUNK_BODY, data.length - offset);
        NSData *chunk = makeChunk('D', i, total, data, offset, len);
        [_peripheralMgr updateValue:chunk
                  forCharacteristic:_dataChar
               onSubscribedCentrals:@[target]];
        [NSThread sleepForTimeInterval:0.01];
    }
}

- (void)sendDataToPeripheral:(CBPeripheral *)p data:(NSData *)data {
    CBCharacteristic *dataC = nil;
    for (CBService *s in p.services)
        for (CBCharacteristic *c in s.characteristics)
            if ([c.UUID isEqual:dataUUID()]) { dataC = c; break; }
    if (!dataC) return;

    uint16_t total = (uint16_t)((data.length + CHUNK_BODY - 1) / CHUNK_BODY);
    for (uint16_t i = 0; i < total; i++) {
        NSUInteger offset = (NSUInteger)i * CHUNK_BODY;
        NSUInteger len    = MIN(CHUNK_BODY, data.length - offset);
        NSData *chunk = makeChunk('D', i, total, data, offset, len);
        [p writeValue:chunk forCharacteristic:dataC type:CBCharacteristicWriteWithoutResponse];
        [NSThread sleepForTimeInterval:0.01];
    }
}
```

At the bottom C API section, add:
```objc
void ble_peripheral_send_data_to(const char *central_uuid, const uint8_t *data, int len) {
    NSString *uuid = @(central_uuid);
    NSData *d = [NSData dataWithBytes:data length:(NSUInteger)len];
    dispatch_async(gBLE.bleQueue, ^{
        [gBLE sendDataToCentral:uuid data:d];
    });
}

void ble_central_send_data(const char *peripheral_uuid, const uint8_t *data, int len) {
    NSUUID *uid = [[NSUUID alloc] initWithUUIDString:@(peripheral_uuid)];
    NSData *d = [NSData dataWithBytes:data length:(NSUInteger)len];
    dispatch_async(gBLE.bleQueue, ^{
        CBPeripheral *p = gBLE.peripherals[uid];
        if (p) [gBLE sendDataToPeripheral:p data:d];
    });
}
```

- [ ] **Step 9: Build check (darwin)**

```bash
cd /Users/chad/Projects/workspace/bdpeer
go build ./...
```

Expected: no errors. The ObjC changes don't affect non-darwin builds.

- [ ] **Step 10: Commit**

```bash
git add internal/discovery/ble_corebluetooth.h internal/discovery/ble_corebluetooth.m
git commit -m "feat(ble): BD9E0004 DataChar — GATT send/receive for bdpeer frames"
```

---

## Task 2: Go BLE layer — data callback and send functions

**Files:**
- Modify: `internal/discovery/ble_darwin.go`
- Modify: `internal/discovery/ble_linux.go`
- Modify: `internal/discovery/ble_windows.go`
- Modify: `internal/discovery/ble_stub.go`

- [ ] **Step 1: Extend bleCallbacks and add SetBLEDataCallback in ble_darwin.go**

In `ble_darwin.go`, update the `bleCallbacks` struct:
```go
var bleCallbacks struct {
    sync.Mutex
    onPeerFound         func(nickname, peerUUID string)
    onSDPReceived       func(peerUUID, sdp string, isOffer bool)
    onCentralSubscribed func(centralUUID string)
    onDataReceived      func(peerUUID string, data []byte) // NEW
}
```

Add the setter:
```go
// SetBLEDataCallback registers the handler for inbound BLE data frames.
// Call before StartBLE. The data slice must not be retained past the callback.
func SetBLEDataCallback(onData func(peerUUID string, data []byte)) {
    bleCallbacks.Lock()
    defer bleCallbacks.Unlock()
    bleCallbacks.onDataReceived = onData
}
```

- [ ] **Step 2: Add BLE send functions in ble_darwin.go**

```go
// BLEPeripheralSendDataTo sends data to a specific central (peripheral→central notify).
func BLEPeripheralSendDataTo(centralUUID string, data []byte) {
    cu := C.CString(centralUUID)
    defer C.free(unsafe.Pointer(cu))
    C.ble_peripheral_send_data_to(cu, (*C.uint8_t)(unsafe.Pointer(&data[0])), C.int(len(data)))
}

// BLECentralSendData sends data to a specific peripheral (central→peripheral write).
func BLECentralSendData(peripheralUUID string, data []byte) {
    pu := C.CString(peripheralUUID)
    defer C.free(unsafe.Pointer(pu))
    C.ble_central_send_data(pu, (*C.uint8_t)(unsafe.Pointer(&data[0])), C.int(len(data)))
}
```

- [ ] **Step 3: Export go_ble_data_received CGo callback in ble_darwin.go**

```go
//export go_ble_data_received
func go_ble_data_received(peerUUID *C.char, data *C.uint8_t, length C.int) {
    bleCallbacks.Lock()
    cb := bleCallbacks.onDataReceived
    bleCallbacks.Unlock()
    if cb == nil || length == 0 {
        return
    }
    // Copy data out of C memory before releasing the CGo stack.
    buf := make([]byte, int(length))
    copy(buf, C.GoBytes(unsafe.Pointer(data), length))
    cb(C.GoString(peerUUID), buf)
}
```

- [ ] **Step 4: Add data stubs to ble_linux.go, ble_windows.go, ble_stub.go**

In `ble_linux.go` (after existing stubs — will be replaced in Task 5):
```go
func SetBLEDataCallback(_ func(string, []byte)) {}
func BLEPeripheralSendDataTo(_ string, _ []byte) {}
func BLECentralSendData(_ string, _ []byte)      {}
```

Same block in `ble_windows.go` and `ble_stub.go`.

- [ ] **Step 5: Build check**

```bash
go build ./...
```

Expected: no errors.

- [ ] **Step 6: Commit**

```bash
git add internal/discovery/ble_darwin.go internal/discovery/ble_linux.go \
        internal/discovery/ble_windows.go internal/discovery/ble_stub.go
git commit -m "feat(ble): Go data callback + BLECentralSendData/BLEPeripheralSendDataTo"
```

---

## Task 3: ChunkAssembler utility (platform-agnostic)

**Files:**
- Create: `internal/discovery/ble_chunk.go`
- Test: `internal/discovery/ble_chunk_test.go`

This is used by Linux (Task 5) for Go-side chunk reassembly, since Linux receives raw BLE notifications (one per chunk) instead of ObjC-reassembled data.

- [ ] **Step 1: Write failing tests for ChunkAssembler**

Create `internal/discovery/ble_chunk_test.go`:
```go
package discovery

import (
    "testing"
)

func TestMakeAndAssembleChunks(t *testing.T) {
    data := make([]byte, 1100) // spans 3 chunks (490+490+120)
    for i := range data {
        data[i] = byte(i % 256)
    }

    chunks := MakeDataChunks(data)
    if len(chunks) != 3 {
        t.Fatalf("want 3 chunks, got %d", len(chunks))
    }

    a := NewChunkAssembler()
    var result []byte
    for _, c := range chunks {
        if got, done := a.Feed("peer1", c); done {
            result = got
        }
    }
    if len(result) != len(data) {
        t.Fatalf("want %d bytes, got %d", len(data), len(result))
    }
    for i, b := range result {
        if b != data[i] {
            t.Fatalf("byte %d: want %d, got %d", i, data[i], b)
        }
    }
}

func TestChunkAssemblerSingleChunk(t *testing.T) {
    data := []byte("hello world")
    chunks := MakeDataChunks(data)
    if len(chunks) != 1 {
        t.Fatalf("want 1 chunk, got %d", len(chunks))
    }

    a := NewChunkAssembler()
    result, done := a.Feed("peer1", chunks[0])
    if !done {
        t.Fatal("single chunk should be done immediately")
    }
    if string(result) != "hello world" {
        t.Fatalf("want 'hello world', got %q", result)
    }
}

func TestChunkAssemblerMultiplePeers(t *testing.T) {
    dataA := []byte("message from A")
    dataB := []byte("message from B")
    chunksA := MakeDataChunks(dataA)
    chunksB := MakeDataChunks(dataB)

    a := NewChunkAssembler()
    resA, doneA := a.Feed("peerA", chunksA[0])
    resB, doneB := a.Feed("peerB", chunksB[0])
    if !doneA || !doneB {
        t.Fatal("single-chunk messages should be done immediately")
    }
    if string(resA) != "message from A" || string(resB) != "message from B" {
        t.Fatalf("peer isolation broken: %q %q", resA, resB)
    }
}
```

- [ ] **Step 2: Run tests to confirm they fail**

```bash
go test ./internal/discovery/... -run TestMakeAndAssemble -v
go test ./internal/discovery/... -run TestChunkAssemblerSingle -v
go test ./internal/discovery/... -run TestChunkAssemblerMultiple -v
```

Expected: compilation error — `MakeDataChunks` and `NewChunkAssembler` undefined.

- [ ] **Step 3: Implement ble_chunk.go**

Create `internal/discovery/ble_chunk.go`:
```go
package discovery

const (
    bleChunkHdr  = 5
    bleChunkBody = 490
    bleDataType  = 'D'
)

// MakeDataChunks splits data into BLE chunk frames for DataChar transmission.
// Each chunk: [type(1)][idx_hi(1)][idx_lo(1)][total_hi(1)][total_lo(1)][payload...]
func MakeDataChunks(data []byte) [][]byte {
    total := (len(data) + bleChunkBody - 1) / bleChunkBody
    if total == 0 {
        total = 1
    }
    chunks := make([][]byte, total)
    for i := 0; i < total; i++ {
        offset := i * bleChunkBody
        end := offset + bleChunkBody
        if end > len(data) {
            end = len(data)
        }
        payload := data[offset:end]
        chunk := make([]byte, bleChunkHdr+len(payload))
        chunk[0] = bleDataType
        chunk[1] = byte(uint16(i) >> 8)
        chunk[2] = byte(uint16(i))
        chunk[3] = byte(uint16(total) >> 8)
        chunk[4] = byte(uint16(total))
        copy(chunk[bleChunkHdr:], payload)
        chunks[i] = chunk
    }
    return chunks
}

// ChunkAssembler reassembles BLE DataChar chunk sequences per peer.
// Not goroutine-safe — the caller must serialize Feed calls per instance.
type ChunkAssembler struct {
    bufs map[string][]byte
}

func NewChunkAssembler() *ChunkAssembler {
    return &ChunkAssembler{bufs: make(map[string][]byte)}
}

// Feed processes one raw BLE notification chunk for the given peer key.
// Returns (assembled data, true) when all chunks for a sequence have arrived.
func (a *ChunkAssembler) Feed(peerKey string, chunk []byte) ([]byte, bool) {
    if len(chunk) < bleChunkHdr {
        return nil, false
    }
    idx   := uint16(chunk[1])<<8 | uint16(chunk[2])
    total := uint16(chunk[3])<<8 | uint16(chunk[4])
    payload := chunk[bleChunkHdr:]

    if idx == 0 {
        a.bufs[peerKey] = make([]byte, 0, int(total)*bleChunkBody)
    }
    a.bufs[peerKey] = append(a.bufs[peerKey], payload...)

    if idx == total-1 {
        result := a.bufs[peerKey]
        delete(a.bufs, peerKey)
        return result, true
    }
    return nil, false
}
```

- [ ] **Step 4: Run tests to confirm they pass**

```bash
go test ./internal/discovery/... -run TestMakeAndAssemble -v
go test ./internal/discovery/... -run TestChunkAssemblerSingle -v
go test ./internal/discovery/... -run TestChunkAssemblerMultiple -v
```

Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/discovery/ble_chunk.go internal/discovery/ble_chunk_test.go
git commit -m "feat(ble): ChunkAssembler + MakeDataChunks for BLE data framing"
```

---

## Task 4: BLETransport

**Files:**
- Create: `internal/transport/ble.go`
- Test: `internal/transport/ble_test.go`

BLETransport implements `Transport` using one `io.Pipe` per peer for inbound data, plus a `sendFn` callback for outbound. No platform-specific code — the platform-specific send functions (Task 2) are passed in as `func([]byte)`.

- [ ] **Step 1: Write failing tests**

Create `internal/transport/ble_test.go`:
```go
package transport

import (
    "bytes"
    "context"
    "encoding/binary"
    "encoding/json"
    "io"
    "testing"
    "time"

    "github.com/hsleedevelop/bdpeer/internal/proto"
)

// encodeFrame returns the [4-byte length][JSON] encoding of f.
func encodeFrame(t *testing.T, f proto.Frame) []byte {
    t.Helper()
    payload, _ := json.Marshal(f)
    buf := make([]byte, 4+len(payload))
    binary.BigEndian.PutUint32(buf[:4], uint32(len(payload)))
    copy(buf[4:], payload)
    return buf
}

func TestBLETransportRoundtrip(t *testing.T) {
    bt := NewBLETransport()

    var sent []byte
    sendFn := func(data []byte) {
        sent = append(sent, data...)
    }

    peer := PeerID("ble-test-peer")
    bt.Attach(peer, sendFn)

    received := make(chan proto.Frame, 1)
    bt.SetHandler(func(from PeerID, stream io.ReadWriteCloser) {
        var length uint32
        binary.Read(stream, binary.BigEndian, &length)
        buf := make([]byte, length)
        io.ReadFull(stream, buf)
        var f proto.Frame
        json.Unmarshal(buf, &f)
        received <- f
    })

    // Simulate inbound data arriving from BLE callback.
    inbound := encodeFrame(t, proto.Frame{Type: proto.FrameText, From: "alice", Content: "hi"})
    bt.InboundData(peer, inbound)

    select {
    case f := <-received:
        if f.Content != "hi" {
            t.Fatalf("want 'hi', got %q", f.Content)
        }
    case <-time.After(time.Second):
        t.Fatal("timeout waiting for inbound frame")
    }

    // Simulate outbound write.
    ctx := context.Background()
    stream, err := bt.OpenStream(ctx, peer)
    if err != nil {
        t.Fatal(err)
    }
    outbound := encodeFrame(t, proto.Frame{Type: proto.FrameText, From: "bob", Content: "hello"})
    stream.Write(outbound)
    stream.Close()

    if !bytes.Equal(sent, outbound) {
        t.Fatalf("outbound data mismatch: want %v, got %v", outbound, sent)
    }
}

func TestBLETransportNoConnection(t *testing.T) {
    bt := NewBLETransport()
    bt.SetHandler(func(_ PeerID, _ io.ReadWriteCloser) {})
    _, err := bt.OpenStream(context.Background(), PeerID("ghost"))
    if err == nil {
        t.Fatal("expected ErrNoConnection")
    }
}

func TestBLETransportClose(t *testing.T) {
    bt := NewBLETransport()
    bt.SetHandler(func(_ PeerID, _ io.ReadWriteCloser) {})

    peer := PeerID("ble-close-test")
    bt.Attach(peer, func(_ []byte) {})
    bt.Close(peer)

    _, err := bt.OpenStream(context.Background(), peer)
    if err == nil {
        t.Fatal("expected error after close")
    }
}
```

- [ ] **Step 2: Run tests to confirm they fail**

```bash
go test ./internal/transport/... -run TestBLETransport -v
```

Expected: compilation error — `NewBLETransport` undefined.

- [ ] **Step 3: Implement BLETransport**

Create `internal/transport/ble.go`:
```go
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
// Write is unused (inbound is read-only from the handler's perspective).
type bleReaderRWC struct {
    entry *bleEntry
}

func (r *bleReaderRWC) Read(p []byte) (int, error)  { return r.entry.pr.Read(p) }
func (r *bleReaderRWC) Write(p []byte) (int, error) { return len(p), nil }
func (r *bleReaderRWC) Close() error                { return nil }
```

- [ ] **Step 4: Run tests to confirm they pass**

```bash
go test ./internal/transport/... -run TestBLETransport -v
```

Expected: all PASS.

- [ ] **Step 5: Full test suite**

```bash
go test ./...
```

Expected: all pass (BLETransport is platform-agnostic, no build tag issues).

- [ ] **Step 6: Commit**

```bash
git add internal/transport/ble.go internal/transport/ble_test.go
git commit -m "feat(transport): BLETransport — io.Pipe + sendFn implements Transport interface"
```

---

## Task 5: Linux/Windows GATT central connect + data

**Files:**
- Modify: `internal/discovery/ble_linux.go`
- Modify: `internal/discovery/ble_windows.go`

Linux and Windows use tinygo/x/bluetooth as GATT central only (no peripheral). After scanning and finding a bdpeer peripheral, they connect, discover DataChar, subscribe to notifications (inbound), and enable writing (outbound). `SetBLEDataCallback` and `BLECentralSendData` are implemented properly (replacing the stubs from Task 2).

**Note:** tinygo Scan is blocking; we use a single adapter and manage connection in the scan callback.

- [ ] **Step 1: Refactor ble_linux.go to add GATT data**

Replace the entire contents of `internal/discovery/ble_linux.go`:
```go
//go:build linux

package discovery

import (
    "context"
    "fmt"
    "sync"

    "tinygo.org/x/bluetooth"
)

var (
    linuxDataMu      sync.Mutex
    linuxDataCb      func(peerUUID string, data []byte)
    linuxDataChars   = map[string]bluetooth.DeviceCharacteristic{} // peerAddr → char
    linuxDataCharsMu sync.RWMutex
    linuxAssembler   = NewChunkAssembler()
)

var (
    bleServiceUUID  = bluetooth.NewUUID([16]byte{0xBD, 0x9E, 0x00, 0x01, 0xF0, 0xF0, 0x10, 0x00, 0x80, 0x00, 0x00, 0x80, 0x5F, 0x9B, 0x34, 0xFB})
    bleNickCharUUID = bluetooth.NewUUID([16]byte{0xBD, 0x9E, 0x00, 0x02, 0xF0, 0xF0, 0x10, 0x00, 0x80, 0x00, 0x00, 0x80, 0x5F, 0x9B, 0x34, 0xFB})
    bleDataCharUUID = bluetooth.NewUUID([16]byte{0xBD, 0x9E, 0x00, 0x04, 0xF0, 0xF0, 0x10, 0x00, 0x80, 0x00, 0x00, 0x80, 0x5F, 0x9B, 0x34, 0xFB})
)

func SetBLECallbacks(_ func(string, string), _ func(string, string, bool), _ func(string)) {}

func SetBLEDataCallback(onData func(peerUUID string, data []byte)) {
    linuxDataMu.Lock()
    linuxDataCb = onData
    linuxDataMu.Unlock()
}

func BLEPeripheralSendDataTo(_ string, _ []byte) {} // Linux is central-only

func BLECentralSendData(peripheralUUID string, data []byte) {
    linuxDataCharsMu.RLock()
    char, ok := linuxDataChars[peripheralUUID]
    linuxDataCharsMu.RUnlock()
    if !ok {
        return
    }
    for _, chunk := range MakeDataChunks(data) {
        char.WriteWithoutResponse(chunk)
    }
}

func BLEPeripheralSendSDP(_ string)        {}
func BLECentralSendSDP(_ string, _ string) {}

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
            if nick == "unknown" {
                return
            }
            peerAddr := d.Address.String()
            mgr.Notify(DiscoveredPeer{Nickname: nick, Addr: peerAddr, Source: "ble"})
            go connectAndSubscribe(ctx, adapter, d, nick, peerAddr, mgr)
        })
    }()
    <-ctx.Done()
    return nil
}

func connectAndSubscribe(ctx context.Context, adapter *bluetooth.Adapter, d bluetooth.ScanResult, nick, peerAddr string, mgr *Manager) {
    linuxDataCharsMu.RLock()
    _, already := linuxDataChars[peerAddr]
    linuxDataCharsMu.RUnlock()
    if already {
        return
    }

    dev, err := adapter.Connect(d.Address, bluetooth.ConnectionParams{})
    if err != nil {
        return
    }
    go func() {
        <-ctx.Done()
        dev.Disconnect()
    }()

    srvcs, err := dev.DiscoverServices([]bluetooth.UUID{bleServiceUUID})
    if err != nil || len(srvcs) == 0 {
        return
    }
    chars, err := srvcs[0].DiscoverCharacteristics([]bluetooth.UUID{bleNickCharUUID, bleDataCharUUID})
    if err != nil {
        return
    }

    var dataChar bluetooth.DeviceCharacteristic
    for _, c := range chars {
        switch c.UUID() {
        case bleNickCharUUID:
            buf := make([]byte, 64)
            n, _ := c.Read(buf)
            if n > 0 {
                mgr.Notify(DiscoveredPeer{Nickname: string(buf[:n]), Addr: peerAddr, Source: "ble"})
            }
        case bleDataCharUUID:
            dataChar = c
        }
    }

    if dataChar.UUID() == (bluetooth.UUID{}) {
        return
    }

    linuxDataCharsMu.Lock()
    linuxDataChars[peerAddr] = dataChar
    linuxDataCharsMu.Unlock()

    dataChar.EnableNotifications(func(buf []byte) {
        if data, done := linuxAssembler.Feed(peerAddr, buf); done {
            linuxDataMu.Lock()
            cb := linuxDataCb
            linuxDataMu.Unlock()
            if cb != nil {
                cb(peerAddr, data)
            }
        }
    })
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

- [ ] **Step 2: Mirror data stubs in ble_windows.go**

In `ble_windows.go`, replace the existing stub implementations for the new functions (keep the body of `StartBLE` and `extractBLENickname` as-is, only update function signatures):
```go
func SetBLEDataCallback(_ func(string, []byte)) {}
func BLEPeripheralSendDataTo(_ string, _ []byte) {}
func BLECentralSendData(_ string, _ []byte)      {}
```

Remove the old stubs for these functions that were added in Task 2.

- [ ] **Step 3: Build check**

```bash
GOOS=linux go build ./internal/discovery/...
GOOS=windows go build ./internal/discovery/...
go build ./...
```

Expected: all succeed.

- [ ] **Step 4: Commit**

```bash
git add internal/discovery/ble_linux.go internal/discovery/ble_windows.go
git commit -m "feat(ble/linux): GATT connect + DataChar subscribe/write for Linux central"
```

---

## Task 6: Service layer wiring (macOS)

**Files:**
- Modify: `internal/core/service.go`
- Modify: `internal/core/service_ble.go`
- Modify: `internal/core/service_ble_stub.go`

This wires `BLETransport` into the service, registers peers on BLE discovery (before WebRTC), sends Hello over BLE, and keeps BLE as fallback when WebRTC times out.

- [ ] **Step 1: Add bleT field to Service struct in service.go**

In `service.go`, update `Service` struct (after `webrtcT`):
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
    bleT     *transport.BLETransport  // NEW: nil on non-darwin
    mgr      *discovery.Manager
}
```

In `NewService`, initialize `bleT`:
```go
func NewService(cfg *config.Config, cfgPath string) *Service {
    return &Service{
        cfg:      cfg,
        cfgPath:  cfgPath,
        events:   make(chan Event, 256),
        registry: transport.NewRegistry(),
        webrtcT:  transport.NewWebRTCTransport(),
        bleT:     transport.NewBLETransport(),  // NEW
    }
}
```

- [ ] **Step 2: Wire BLETransport handler in service_ble.go**

In `startBLEWithWebRTC`, after `upgrader.OnLog = s.log`, add:

```go
// Wire BLE data transport handler — same inbound processor as libp2p/WebRTC.
s.bleT.SetHandler(s.onInboundStream)

// Route incoming BLE data to the transport.
discovery.SetBLEDataCallback(func(peerUUID string, data []byte) {
    s.bleT.InboundData(transport.PeerID("ble-"+peerUUID), data)
})
```

- [ ] **Step 3: Register BLE transport on peer found (initiator path)**

In `upgrader.OnBLEPeerFound` callback wiring in `startBLEWithWebRTC`, extend the SetBLECallbacks first arg:
```go
discovery.SetBLECallbacks(
    func(nickname, peerUUID string) {
        peerKey := transport.PeerID("ble-" + peerUUID)
        // Register BLE data transport immediately as fallback.
        sendFn := func(data []byte) {
            discovery.BLECentralSendData(peerUUID, data)
        }
        s.bleT.Attach(peerKey, sendFn)
        s.registry.Register(peerKey, s.bleT)
        // Send Hello over BLE so the responder gets our nickname.
        go s.sendBLEHello(ctx, peerKey)
        // Start WebRTC upgrade — will overwrite BLE in registry if it succeeds.
        upgrader.OnBLEPeerFound(ctx, nickname, peerUUID)
    },
    func(peerUUID, sdp string, isOffer bool) {
        upgrader.OnSDPReceived(ctx, peerUUID, sdp, isOffer)
    },
    func(centralUUID string) {
        // Responder path: register BLE data transport keyed by central UUID.
        peerKey := transport.PeerID("ble-" + centralUUID)
        sendFn := func(data []byte) {
            discovery.BLEPeripheralSendDataTo(centralUUID, data)
        }
        s.bleT.Attach(peerKey, sendFn)
        s.registry.Register(peerKey, s.bleT)
        go s.sendBLEHello(ctx, peerKey)
        s.log("[BLE] central 구독: " + centralUUID)
    },
)
```

- [ ] **Step 4: Add sendBLEHello helper in service_ble.go**

```go
func (s *Service) sendBLEHello(ctx context.Context, peerKey transport.PeerID) {
    stream, err := s.bleT.OpenStream(ctx, peerKey)
    if err != nil {
        return
    }
    defer stream.Close()
    _ = transfer.WriteFrame(stream, proto.Frame{Type: proto.FrameHello, From: s.cfg.Nickname})
}
```

- [ ] **Step 5: Keep BLE in registry when WebRTC times out**

In `handleWebRTCConn`, the existing code calls `s.registry.Register(peerKey, s.webrtcT)` which overwrites the BLE entry. This is intentional: WebRTC wins when it connects. No change needed for the timeout case — if WebRTC times out, `OnConnected` never fires, so `registry.Register` for WebRTC never runs. The BLE entry remains. ✓

However, ensure that `conn.OnClose` in `handleWebRTCConn` does not unregister the BLE fallback if WebRTC closes. Review and verify the existing `OnClose`:
```go
conn.OnClose = func() {
    s.registry.Unregister(peerKey)  // removes WebRTC entry
    _ = s.webrtcT.Close(peerKey)
    // BLE entry is under the same peerKey for initiator — this removes it too!
}
```

Fix: when WebRTC connects, the BLE transport entry for this peer should be detached (it's superseded). Add after `s.registry.Register(peerKey, s.webrtcT)`:
```go
_ = s.bleT.Close(peerKey) // BLE superseded by WebRTC for this peer
```

And in `conn.OnClose`, also re-register BLE if it's still attached... actually, simpler: just leave the WebRTC Unregister as-is. If WebRTC closes after it was the active transport, the peer is gone. BLE was already closed when WebRTC took over.

- [ ] **Step 6: Update service_ble_stub.go**

In `service_ble_stub.go` (`//go:build !darwin`), ensure `startBLEWithWebRTC` stub does not reference `s.bleT` in a way that causes compile errors (it's a no-op stub, so no change needed if `bleT` is initialized in `NewService` which is platform-agnostic).

Verify the stub compiles:
```bash
GOOS=linux go build ./internal/core/...
GOOS=windows go build ./internal/core/...
```

- [ ] **Step 7: Wire BLETransport in non-darwin service layer for Linux data**

In `service_ble_stub.go`, add a `startBLEDataTransport` for Linux/Windows (called from wherever BLE is started on those platforms):

Actually, looking at the existing code: `service_ble_stub.go` only has `startBLEWithWebRTC` as a no-op. But on Linux/Windows, BLE data should work. The service currently does not start BLE on Linux in `service_ble_stub.go` — it would need to.

Check `service.go` to see where `startBLEWithWebRTC` is called and how to add Linux support:
```go
// In service.go Start() method (or wherever BLE is invoked):
// Currently: s.startBLEWithWebRTC(ctx, mgr)
// The stub does nothing for non-darwin.
```

For Linux, we need to:
1. Start BLE scan (already works from `discovery.StartBLE` for Linux)
2. Wire `SetBLEDataCallback` → `s.bleT.InboundData`
3. Wire `s.bleT.SetHandler(s.onInboundStream)`

Add a `startBLEData` method in `service_ble_stub.go` for non-darwin platforms that handles the data wiring without WebRTC:

```go
//go:build !darwin

package core

import (
    "context"

    "github.com/hsleedevelop/bdpeer/internal/discovery"
    "github.com/hsleedevelop/bdpeer/internal/transport"
)

func (s *Service) startBLEWithWebRTC(ctx context.Context, mgr *discovery.Manager) {
    s.bleT.SetHandler(s.onInboundStream)
    discovery.SetBLEDataCallback(func(peerUUID string, data []byte) {
        peerKey := transport.PeerID("ble-" + peerUUID)
        // Register BLE transport for this peer on first data received.
        if _, ok := s.registry.Lookup(peerKey); !ok {
            sendFn := func(d []byte) {
                discovery.BLECentralSendData(peerUUID, d)
            }
            s.bleT.Attach(peerKey, sendFn)
            s.registry.Register(peerKey, s.bleT)
        }
        s.bleT.InboundData(peerKey, data)
    })
}
```

- [ ] **Step 8: Full build and test**

```bash
go build ./...
go test ./...
GOOS=linux go build ./...
```

Expected: all pass. Darwin build includes CGo BLE; Linux build excludes it but compiles cleanly.

- [ ] **Step 9: Commit**

```bash
git add internal/core/service.go internal/core/service_ble.go internal/core/service_ble_stub.go
git commit -m "feat(core): wire BLETransport as data channel fallback for WebRTC"
```

---

## Task 7: Smoke test and tag

- [ ] **Step 1: Full test suite**

```bash
go test ./...
```

Expected: all PASS.

- [ ] **Step 2: Darwin build**

```bash
go build -o /tmp/bdpeer-phase2 ./cmd/...
```

Expected: succeeds with CoreBluetooth BLE.

- [ ] **Step 3: Linux cross-compile check**

```bash
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build ./... 2>&1 | grep -v "^$"
```

Expected: no errors (tinygo bluetooth is included, no CGo on Linux).

- [ ] **Step 4: Verify BLE data transport appears in logs**

Run on macOS:
```bash
/tmp/bdpeer-phase2
```

Expected: on BLE peer discovery, log output includes:
- `[BLE] <nickname> 발견 → WebRTC offer 생성 중` (existing)
- BLE transport registered before WebRTC attempt
- If WebRTC succeeds: `[BLE▸WTC] 연결 완료`
- If WebRTC times out: peer remains reachable via BLE data transport

- [ ] **Step 5: Commit and tag**

```bash
git add -A
git commit -m "feat: Phase 2 BLE data transport complete — macOS+Linux GATT DataChar"
git tag v0.4.0
git push origin feat/bdpeer-tui-implementation --tags
```

---

## Self-Review

### Spec coverage
| Requirement | Task |
|---|---|
| Text + chunking protocol | Task 1 (ObjC), Task 3 (ChunkAssembler), Task 4 (BLETransport.Write) |
| macOS ↔ macOS data | Task 1 + 2 + 6 (initiator + responder paths) |
| macOS ↔ Linux data | Task 1 + 5 + 6 |
| WebRTC fallback (BLE stays when WebRTC times out) | Task 6 Step 5 |
| Nickname exchange | Task 6 Step 4 (Hello frame over BLE) |
| Existing WebRTC flow unbroken | Task 6 Step 5 (WebRTC overwrites BLE in registry) |

### Known limitations (accepted for Phase 2)
- Linux → macOS data works. macOS → Linux requires macOS to know which `centralUUID` corresponds to the Linux device (resolved via `go_ble_central_subscribed` + Hello frame).
- Windows receives the same treatment as Linux (central-only via tinygo) but is untested without real hardware.
- BLE throughput (~10 KB/s) limits practical message length to short text. Files are not supported.
