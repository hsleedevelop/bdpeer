// ble_corebluetooth.m — CoreBluetooth peripheral + central for bdpeer BLE signaling.
//go:build darwin

#import <Foundation/Foundation.h>
#import <CoreBluetooth/CoreBluetooth.h>
#include "ble_corebluetooth.h"

// Forward-declare Go callbacks (defined via //export in ble_darwin.go).
extern void go_ble_peer_found(const char *nickname, const char *peer_uuid);
extern void go_ble_sdp_received(const char *peer_uuid, const char *sdp, int is_offer);
extern void go_ble_central_subscribed(const char *central_uuid);
extern void go_ble_data_received(const char *peer_uuid, const uint8_t *data, int len);

// ── UUIDs ─────────────────────────────────────────────────────────────────────
static NSString *const kServiceUUIDStr  = @"BD9E0001-F0F0-1000-8000-00805F9B34FB";
static NSString *const kNickCharUUIDStr = @"BD9E0002-F0F0-1000-8000-00805F9B34FB";
static NSString *const kSDPCharUUIDStr  = @"BD9E0003-F0F0-1000-8000-00805F9B34FB";
static NSString *const kDataCharUUIDStr = @"BD9E0004-F0F0-1000-8000-00805F9B34FB";

static CBUUID *svcUUID(void)  { return [CBUUID UUIDWithString:kServiceUUIDStr];  }
static CBUUID *nickUUID(void) { return [CBUUID UUIDWithString:kNickCharUUIDStr]; }
static CBUUID *sdpUUID(void)  { return [CBUUID UUIDWithString:kSDPCharUUIDStr];  }
static CBUUID *dataUUID(void) { return [CBUUID UUIDWithString:kDataCharUUIDStr]; }

// ── Chunk protocol ────────────────────────────────────────────────────────────
// Header: [type(1)] [idx_hi(1)] [idx_lo(1)] [total_hi(1)] [total_lo(1)]
// Payload: up to 490 bytes
#define CHUNK_HDR  5
#define CHUNK_BODY 490

static NSData *makeChunk(char type, uint16_t idx, uint16_t total, NSData *payload, NSUInteger offset, NSUInteger length) {
    NSMutableData *d = [NSMutableData dataWithCapacity:CHUNK_HDR + length];
    uint8_t hdr[CHUNK_HDR] = {
        (uint8_t)type,
        (uint8_t)(idx >> 8), (uint8_t)(idx & 0xFF),
        (uint8_t)(total >> 8), (uint8_t)(total & 0xFF)
    };
    [d appendBytes:hdr length:CHUNK_HDR];
    [d appendBytes:(const uint8_t *)payload.bytes + offset length:length];
    return d;
}

// ── BDPeerBLE ─────────────────────────────────────────────────────────────────
@interface BDPeerBLE : NSObject <CBPeripheralManagerDelegate, CBCentralManagerDelegate, CBPeripheralDelegate>

@property (nonatomic, copy) NSString *myNickname;
@property (nonatomic, strong) dispatch_queue_t bleQueue;

// Peripheral side
@property (nonatomic, strong) CBPeripheralManager      *peripheralMgr;
@property (nonatomic, strong) CBMutableCharacteristic  *sdpChar;
@property (nonatomic, strong) CBMutableCharacteristic  *dataChar;
@property (nonatomic, strong) NSMutableSet<CBCentral *> *subscribedCentrals;
@property (nonatomic, strong) NSMutableDictionary<NSString *, NSMutableData *> *centralSDPBufs;
@property (nonatomic, strong) NSMutableDictionary<NSString *, NSMutableData *> *centralDataBufs;
// Pending notify queue — drained when peripheralManagerIsReadyToUpdateSubscribers fires.
// Each entry: @{ @"chunk": NSData, @"char": CBMutableCharacteristic, @"centrals": NSArray<CBCentral*>|NSNull }
@property (nonatomic, strong) NSMutableArray<NSDictionary *> *pendingNotifies;

// Central side
@property (nonatomic, strong) CBCentralManager *centralMgr;
@property (nonatomic, strong) NSMutableDictionary<NSUUID *, CBPeripheral *> *peripherals;
@property (nonatomic, strong) NSMutableDictionary<NSString *, NSMutableData *> *peripheralSDPBufs;
@property (nonatomic, strong) NSMutableDictionary<NSString *, NSMutableData *> *peripheralDataBufs;

@end

@implementation BDPeerBLE

- (instancetype)initWithNickname:(NSString *)nickname {
    if (!(self = [super init])) return nil;
    _myNickname         = nickname;
    _subscribedCentrals = [NSMutableSet new];
    _centralSDPBufs     = [NSMutableDictionary new];
    _centralDataBufs    = [NSMutableDictionary new];
    _pendingNotifies    = [NSMutableArray new];
    _peripherals        = [NSMutableDictionary new];
    _peripheralSDPBufs  = [NSMutableDictionary new];
    _peripheralDataBufs = [NSMutableDictionary new];
    return self;
}

- (void)start {
    _bleQueue      = dispatch_queue_create("bdpeer.ble", DISPATCH_QUEUE_SERIAL);
    _peripheralMgr = [[CBPeripheralManager alloc] initWithDelegate:self queue:_bleQueue];
    _centralMgr    = [[CBCentralManager    alloc] initWithDelegate:self queue:_bleQueue];
}

- (void)stop {
    [_peripheralMgr stopAdvertising];
    [_centralMgr    stopScan];
}

// ── Peripheral ────────────────────────────────────────────────────────────────

- (void)peripheralManagerDidUpdateState:(CBPeripheralManager *)pm {
    if (pm.state != CBManagerStatePoweredOn) return;

    CBMutableCharacteristic *nickChar = [[CBMutableCharacteristic alloc]
        initWithType:nickUUID()
          properties:CBCharacteristicPropertyRead
               value:[_myNickname dataUsingEncoding:NSUTF8StringEncoding]
         permissions:CBAttributePermissionsReadable];

    _sdpChar = [[CBMutableCharacteristic alloc]
        initWithType:sdpUUID()
          properties:CBCharacteristicPropertyWriteWithoutResponse | CBCharacteristicPropertyNotify
               value:nil
         permissions:CBAttributePermissionsWriteable];

    _dataChar = [[CBMutableCharacteristic alloc]
        initWithType:dataUUID()
          properties:CBCharacteristicPropertyWriteWithoutResponse | CBCharacteristicPropertyNotify
               value:nil
         permissions:CBAttributePermissionsWriteable];

    CBMutableService *svc = [[CBMutableService alloc] initWithType:svcUUID() primary:YES];
    svc.characteristics = @[nickChar, _sdpChar, _dataChar];
    [pm addService:svc];
}

- (void)peripheralManager:(CBPeripheralManager *)pm didAddService:(CBService *)service error:(NSError *)error {
    if (error) return;
    [pm startAdvertising:@{
        CBAdvertisementDataServiceUUIDsKey: @[svcUUID()],
        CBAdvertisementDataLocalNameKey: _myNickname
    }];
}

- (void)peripheralManager:(CBPeripheralManager *)pm
                  central:(CBCentral *)central
didSubscribeToCharacteristic:(CBCharacteristic *)characteristic {
    if (![characteristic.UUID isEqual:sdpUUID()] &&
        ![characteristic.UUID isEqual:dataUUID()]) return;
    [_subscribedCentrals addObject:central];
    // Fire on DataChar subscription so the responder's BLE Hello notify
    // is delivered (CoreBluetooth drops notifications to non-subscribers).
    // SDP exchange does not depend on this callback — it uses _subscribedCentrals directly.
    if ([characteristic.UUID isEqual:dataUUID()]) {
        go_ble_central_subscribed([central.identifier.UUIDString UTF8String]);
    }
}

- (void)peripheralManager:(CBPeripheralManager *)pm
                  central:(CBCentral *)central
didUnsubscribeFromCharacteristic:(CBCharacteristic *)characteristic {
    if ([characteristic.UUID isEqual:sdpUUID()] ||
        [characteristic.UUID isEqual:dataUUID()]) {
        [_subscribedCentrals removeObject:central];
    }
}

- (void)peripheralManager:(CBPeripheralManager *)pm
    didReceiveWriteRequests:(NSArray<CBATTRequest *> *)requests {
    for (CBATTRequest *req in requests) {
        if ([req.characteristic.UUID isEqual:sdpUUID()]) {
            [self handleChunk:req.value
                     inBufMap:_centralSDPBufs
                          key:req.central.identifier.UUIDString];
        } else if ([req.characteristic.UUID isEqual:dataUUID()]) {
            [self handleDataChunk:req.value
                         inBufMap:_centralDataBufs
                              key:req.central.identifier.UUIDString];
        }
    }
}

// Try to send chunk via updateValue. If CoreBluetooth's transmit queue is full,
// queue the chunk (and all remaining chunks) for retry from peripheralManagerIsReadyToUpdateSubscribers:.
// Returns YES if sent immediately, NO if queued.
- (BOOL)notifyOrQueueChunk:(NSData *)chunk
                   forChar:(CBMutableCharacteristic *)ch
                toCentrals:(NSArray<CBCentral *> *)centrals {
    BOOL ok = [_peripheralMgr updateValue:chunk forCharacteristic:ch onSubscribedCentrals:centrals];
    if (ok) return YES;
    [_pendingNotifies addObject:@{
        @"chunk":    chunk,
        @"char":     ch,
        @"centrals": centrals ?: (id)[NSNull null],
    }];
    return NO;
}

// Send SDP answer to all subscribed centrals.
- (void)sendSDPToAllCentrals:(NSString *)sdp type:(char)type {
    NSData *raw = [sdp dataUsingEncoding:NSUTF8StringEncoding];
    uint16_t total = (uint16_t)((raw.length + CHUNK_BODY - 1) / CHUNK_BODY);
    NSArray *centrals = _subscribedCentrals.allObjects;
    for (uint16_t i = 0; i < total; i++) {
        NSUInteger offset = (NSUInteger)i * CHUNK_BODY;
        NSUInteger len    = MIN(CHUNK_BODY, raw.length - offset);
        NSData *chunk = makeChunk(type, i, total, raw, offset, len);
        if (![self notifyOrQueueChunk:chunk forChar:_sdpChar toCentrals:centrals]) {
            // Queue the remaining chunks as well — they must arrive in order.
            for (uint16_t j = i + 1; j < total; j++) {
                NSUInteger off2 = (NSUInteger)j * CHUNK_BODY;
                NSUInteger l2   = MIN(CHUNK_BODY, raw.length - off2);
                NSData *c2 = makeChunk(type, j, total, raw, off2, l2);
                [_pendingNotifies addObject:@{
                    @"chunk":    c2,
                    @"char":     _sdpChar,
                    @"centrals": centrals ?: (id)[NSNull null],
                }];
            }
            return;
        }
        [NSThread sleepForTimeInterval:0.01]; // pacing
    }
}

// CoreBluetooth signals it can accept more notifications. Drain the pending queue.
- (void)peripheralManagerIsReadyToUpdateSubscribers:(CBPeripheralManager *)pm {
    while (_pendingNotifies.count > 0) {
        NSDictionary *e = _pendingNotifies.firstObject;
        NSData *chunk = e[@"chunk"];
        CBMutableCharacteristic *ch = e[@"char"];
        id rawCentrals = e[@"centrals"];
        NSArray *centrals = (rawCentrals == [NSNull null]) ? nil : rawCentrals;
        if (![pm updateValue:chunk forCharacteristic:ch onSubscribedCentrals:centrals]) {
            return; // queue full again — wait for next ready callback
        }
        [_pendingNotifies removeObjectAtIndex:0];
    }
}

// ── Central ───────────────────────────────────────────────────────────────────

- (void)centralManagerDidUpdateState:(CBCentralManager *)cm {
    if (cm.state != CBManagerStatePoweredOn) return;
    [cm scanForPeripheralsWithServices:@[svcUUID()] options:nil];
}

- (void)centralManager:(CBCentralManager *)cm
 didDiscoverPeripheral:(CBPeripheral *)p
     advertisementData:(NSDictionary *)ad
                  RSSI:(NSNumber *)RSSI {
    if (_peripherals[p.identifier]) return; // already known
    _peripherals[p.identifier] = p;
    [cm connectPeripheral:p options:nil];
}

- (void)centralManager:(CBCentralManager *)cm didConnectPeripheral:(CBPeripheral *)p {
    p.delegate = self;
    [p discoverServices:@[svcUUID()]];
}

- (void)centralManager:(CBCentralManager *)cm didFailToConnectPeripheral:(CBPeripheral *)p error:(NSError *)e {
    [_peripherals removeObjectForKey:p.identifier];
}

- (void)peripheral:(CBPeripheral *)p didDiscoverServices:(NSError *)e {
    if (e) return;
    for (CBService *s in p.services)
        [p discoverCharacteristics:@[nickUUID(), sdpUUID(), dataUUID()] forService:s];
}

- (void)peripheral:(CBPeripheral *)p
didDiscoverCharacteristicsForService:(CBService *)s
             error:(NSError *)e {
    if (e) return;
    for (CBCharacteristic *c in s.characteristics) {
        if ([c.UUID isEqual:nickUUID()])  [p readValueForCharacteristic:c];
        if ([c.UUID isEqual:sdpUUID()])   [p setNotifyValue:YES forCharacteristic:c];
        if ([c.UUID isEqual:dataUUID()])  [p setNotifyValue:YES forCharacteristic:c];
    }
}

- (void)peripheral:(CBPeripheral *)p
didUpdateValueForCharacteristic:(CBCharacteristic *)c
             error:(NSError *)e {
    if (e || !c.value) return;

    if ([c.UUID isEqual:nickUUID()]) {
        NSString *nick = [[NSString alloc] initWithData:c.value encoding:NSUTF8StringEncoding];
        go_ble_peer_found([nick UTF8String], [p.identifier.UUIDString UTF8String]);
        return;
    }

    if ([c.UUID isEqual:sdpUUID()]) {
        [self handleChunk:c.value
                 inBufMap:_peripheralSDPBufs
                      key:p.identifier.UUIDString];
    }

    if ([c.UUID isEqual:dataUUID()]) {
        [self handleDataChunk:c.value
                     inBufMap:_peripheralDataBufs
                          key:p.identifier.UUIDString];
    }
}

// Send SDP offer chunks to a specific peripheral.
- (void)sendSDPToPeripheral:(CBPeripheral *)p sdp:(NSString *)sdp type:(char)type {
    CBCharacteristic *sdpC = nil;
    for (CBService *s in p.services)
        for (CBCharacteristic *c in s.characteristics)
            if ([c.UUID isEqual:sdpUUID()]) { sdpC = c; break; }
    if (!sdpC) return;

    NSData *raw = [sdp dataUsingEncoding:NSUTF8StringEncoding];
    uint16_t total = (uint16_t)((raw.length + CHUNK_BODY - 1) / CHUNK_BODY);
    for (uint16_t i = 0; i < total; i++) {
        NSUInteger offset = (NSUInteger)i * CHUNK_BODY;
        NSUInteger len    = MIN(CHUNK_BODY, raw.length - offset);
        NSData *chunk = makeChunk(type, i, total, raw, offset, len);
        [p writeValue:chunk forCharacteristic:sdpC type:CBCharacteristicWriteWithoutResponse];
        [NSThread sleepForTimeInterval:0.01];
    }
}

// ── Shared chunk assembly ─────────────────────────────────────────────────────

- (void)handleChunk:(NSData *)chunk
           inBufMap:(NSMutableDictionary<NSString *, NSMutableData *> *)bufs
                key:(NSString *)key {
    if (chunk.length < CHUNK_HDR) return;
    const uint8_t *b = chunk.bytes;
    char     type  = (char)b[0];
    uint16_t idx   = ((uint16_t)b[1] << 8) | b[2];
    uint16_t total = ((uint16_t)b[3] << 8) | b[4];

    if (!bufs[key]) bufs[key] = [NSMutableData new];
    [bufs[key] appendBytes:b + CHUNK_HDR length:chunk.length - CHUNK_HDR];

    if (idx == total - 1) {
        NSString *sdp = [[NSString alloc] initWithData:bufs[key] encoding:NSUTF8StringEncoding];
        bufs[key] = nil;
        go_ble_sdp_received([key UTF8String], [sdp UTF8String], type == 'O' ? 1 : 0);
    }
}

- (void)handleDataChunk:(NSData *)chunk
               inBufMap:(NSMutableDictionary<NSString *, NSMutableData *> *)bufs
                    key:(NSString *)key {
    if (chunk.length < CHUNK_HDR) return;
    const uint8_t *b = chunk.bytes;
    if ((char)b[0] != 'D') return;
    uint16_t idx   = ((uint16_t)b[1] << 8) | b[2];
    uint16_t total = ((uint16_t)b[3] << 8) | b[4];
    if (total == 0) return;
    if (idx != 0 && !bufs[key]) return;

    if (!bufs[key]) bufs[key] = [NSMutableData new];
    [bufs[key] appendBytes:b + CHUNK_HDR length:chunk.length - CHUNK_HDR];

    if (idx == total - 1) {
        NSData *assembled = bufs[key];
        bufs[key] = nil;
        go_ble_data_received([key UTF8String], (const uint8_t *)assembled.bytes, (int)assembled.length);
    }
}

// ── Data send helpers ─────────────────────────────────────────────────────────

- (void)sendDataToCentral:(NSString *)centralUUID data:(NSData *)data {
    if (data.length == 0) return;
    CBCentral *target = nil;
    for (CBCentral *c in _subscribedCentrals) {
        if ([c.identifier.UUIDString isEqualToString:centralUUID]) {
            target = c;
            break;
        }
    }
    if (!target) return;

    NSArray *centrals = @[target];
    uint16_t total = (uint16_t)((data.length + CHUNK_BODY - 1) / CHUNK_BODY);
    for (uint16_t i = 0; i < total; i++) {
        NSUInteger offset = (NSUInteger)i * CHUNK_BODY;
        NSUInteger len    = MIN(CHUNK_BODY, data.length - offset);
        NSData *chunk = makeChunk('D', i, total, data, offset, len);
        BOOL sent = [self notifyOrQueueChunk:chunk forChar:_dataChar toCentrals:centrals];
        if (!sent) {
            for (uint16_t j = i + 1; j < total; j++) {
                NSUInteger off2 = (NSUInteger)j * CHUNK_BODY;
                NSUInteger l2   = MIN(CHUNK_BODY, data.length - off2);
                NSData *c2 = makeChunk('D', j, total, data, off2, l2);
                [_pendingNotifies addObject:@{
                    @"chunk":    c2,
                    @"char":     _dataChar,
                    @"centrals": centrals,
                }];
            }
            return;
        }
        [NSThread sleepForTimeInterval:0.01];
    }
}

- (void)sendDataToPeripheral:(CBPeripheral *)p data:(NSData *)data {
    if (data.length == 0) return;
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

@end

// ── C API ─────────────────────────────────────────────────────────────────────

static BDPeerBLE *gBLE = nil;

void ble_start(const char *nickname) {
    @autoreleasepool {
        gBLE = [[BDPeerBLE alloc] initWithNickname:@(nickname)];
        [gBLE start];
        // Keep the thread's run loop alive.
        [[NSRunLoop currentRunLoop] run];
    }
}

void ble_stop(void) {
    [gBLE stop];
    gBLE = nil;
    CFRunLoopStop(CFRunLoopGetCurrent());
}

void ble_peripheral_send_sdp(const char *sdp, char sdp_type) {
    // Dispatch to the BLE queue — CoreBluetooth requires calls on its designated queue.
    NSString *sdpStr = @(sdp);
    char type = sdp_type;
    dispatch_async(gBLE.bleQueue, ^{
        [gBLE sendSDPToAllCentrals:sdpStr type:type];
    });
}

void ble_central_send_sdp(const char *peer_uuid, const char *sdp, char sdp_type) {
    NSUUID *uid = [[NSUUID alloc] initWithUUIDString:@(peer_uuid)];
    NSString *sdpStr = @(sdp);
    char type = sdp_type;
    dispatch_async(gBLE.bleQueue, ^{
        CBPeripheral *p = gBLE.peripherals[uid];
        if (p) [gBLE sendSDPToPeripheral:p sdp:sdpStr type:type];
    });
}

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
