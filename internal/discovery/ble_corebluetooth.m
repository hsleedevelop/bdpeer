// ble_corebluetooth.m — CoreBluetooth peripheral + central for bdpeer BLE signaling.
//go:build darwin

#import <Foundation/Foundation.h>
#import <CoreBluetooth/CoreBluetooth.h>
#include <stdlib.h>
#include "ble_corebluetooth.h"

// Forward-declare Go callbacks (defined via //export in ble_darwin.go).
extern void go_ble_peer_found(const char *nickname, const char *peer_uuid);
extern void go_ble_sdp_received(const char *peer_uuid, const char *sdp, int is_offer);
extern void go_ble_central_subscribed(const char *central_uuid);
extern void go_ble_data_received(const char *peer_uuid, const uint8_t *data, int len);
extern void go_ble_log(const char *msg);

// ── UUIDs ─────────────────────────────────────────────────────────────────────
static NSString *const kServiceUUIDStr  = @"BD9E0001-F0F0-1000-8000-00805F9B34FB";
static NSString *const kNickCharUUIDStr = @"BD9E0002-F0F0-1000-8000-00805F9B34FB";
static NSString *const kSDPCharUUIDStr  = @"BD9E0003-F0F0-1000-8000-00805F9B34FB";
static NSString *const kDataCharUUIDStr = @"BD9E0004-F0F0-1000-8000-00805F9B34FB";

static CBUUID *svcUUID(void)  { return [CBUUID UUIDWithString:kServiceUUIDStr];  }
static CBUUID *nickUUID(void) { return [CBUUID UUIDWithString:kNickCharUUIDStr]; }
static CBUUID *sdpUUID(void)  { return [CBUUID UUIDWithString:kSDPCharUUIDStr];  }
static CBUUID *dataUUID(void) { return [CBUUID UUIDWithString:kDataCharUUIDStr]; }

static void bd_ble_log(NSString *msg) {
    if (!msg) return;
    go_ble_log([msg UTF8String]);
}

static int bd_startup_scan_seconds(void) {
    const char *raw = getenv("BDPEER_DARWIN_BLE_STARTUP_SCAN_SECONDS");
    if (!raw || raw[0] == '\0') return 0;
    int seconds = atoi(raw);
    return seconds > 0 ? seconds : 0;
}

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
@property (nonatomic, strong) NSMutableSet<NSString *> *readyPeripheralIDs;
@property (nonatomic, strong) NSMutableSet<NSString *> *connectingPeripheralIDs;
@property (nonatomic, strong) NSMutableDictionary<NSString *, NSString *> *peripheralNames;
@property (nonatomic, strong) NSMutableDictionary<NSString *, NSString *> *notifiedPeripheralNames;
@property (nonatomic, strong) NSMutableDictionary<NSString *, NSNumber *> *connectRetryCounts;
@property (nonatomic, strong) NSMutableDictionary<NSString *, NSNumber *> *serviceDiscoveryRetryCounts;
@property (nonatomic, strong) NSMutableDictionary<NSString *, NSMutableData *> *peripheralSDPBufs;
@property (nonatomic, strong) NSMutableDictionary<NSString *, NSMutableData *> *peripheralDataBufs;
@property (nonatomic, assign) BOOL startupScanStarted;
@property (nonatomic, assign) BOOL recoveryScanScheduled;

@end

@implementation BDPeerBLE

- (instancetype)initWithNickname:(NSString *)nickname {
    if (!(self = [super init])) return nil;
    _myNickname         = [nickname copy];
    _subscribedCentrals = [NSMutableSet new];
    _centralSDPBufs     = [NSMutableDictionary new];
    _centralDataBufs    = [NSMutableDictionary new];
    _pendingNotifies    = [NSMutableArray new];
    _peripherals        = [NSMutableDictionary new];
    _readyPeripheralIDs = [NSMutableSet new];
    _connectingPeripheralIDs = [NSMutableSet new];
    _peripheralNames    = [NSMutableDictionary new];
    _notifiedPeripheralNames = [NSMutableDictionary new];
    _connectRetryCounts = [NSMutableDictionary new];
    _serviceDiscoveryRetryCounts = [NSMutableDictionary new];
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
    if (pm.state != CBManagerStatePoweredOn) {
        bd_ble_log([NSString stringWithFormat:@"peripheral state not powered: %ld", (long)pm.state]);
        return;
    }
    bd_ble_log(@"peripheral powered on; adding service");

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
    if (error) {
        bd_ble_log([NSString stringWithFormat:@"service add failed: %@", error.localizedDescription]);
        return;
    }
    [pm startAdvertising:@{
        CBAdvertisementDataServiceUUIDsKey: @[svcUUID()],
        CBAdvertisementDataLocalNameKey: _myNickname
    }];
}

- (void)peripheralManagerDidStartAdvertising:(CBPeripheralManager *)pm error:(NSError *)error {
    if (error) {
        bd_ble_log([NSString stringWithFormat:@"advertising failed: %@", error.localizedDescription]);
        return;
    }
    bd_ble_log([NSString stringWithFormat:@"advertising started: %@", _myNickname]);
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
        // Drop any partial chunk-reassembly state for this central so a future
        // reconnect cannot inherit stale bytes mid-message.
        NSString *uuid = central.identifier.UUIDString;
        [_centralSDPBufs  removeObjectForKey:uuid];
        [_centralDataBufs removeObjectForKey:uuid];
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
    if (cm.state != CBManagerStatePoweredOn) {
        bd_ble_log([NSString stringWithFormat:@"central state not powered: %ld", (long)cm.state]);
        return;
    }

    int seconds = bd_startup_scan_seconds();
    if (seconds <= 0 || _startupScanStarted) return;
    _startupScanStarted = YES;
    bd_ble_log([NSString stringWithFormat:@"startup scan enabled for %d seconds", seconds]);
    [self startCentralScan];
    dispatch_after(dispatch_time(DISPATCH_TIME_NOW, (int64_t)seconds * NSEC_PER_SEC), _bleQueue, ^{
        [_centralMgr stopScan];
    });
}

- (void)startCentralScan {
    if (_centralMgr.state != CBManagerStatePoweredOn) {
        bd_ble_log([NSString stringWithFormat:@"scan skipped; central state: %ld", (long)_centralMgr.state]);
        return;
    }
    bd_ble_log(@"scan started");
    [_centralMgr scanForPeripheralsWithServices:@[svcUUID()] options:nil];
}

- (void)markPeripheralUnready:(NSUUID *)identifier {
    if (!identifier) return;
    NSString *uuid = identifier.UUIDString;
    [_readyPeripheralIDs removeObject:uuid];
    [_connectingPeripheralIDs removeObject:uuid];
    [_peripheralSDPBufs  removeObjectForKey:uuid];
    [_peripheralDataBufs removeObjectForKey:uuid];
    [_notifiedPeripheralNames removeObjectForKey:uuid];
    [_connectRetryCounts removeObjectForKey:uuid];
    [_serviceDiscoveryRetryCounts removeObjectForKey:uuid];
}

- (void)dropPeripheral:(CBPeripheral *)p reason:(NSString *)reason {
    if (!p) return;
    NSString *uuid = p.identifier.UUIDString;
    bd_ble_log([NSString stringWithFormat:@"%@: %@ state=%ld", reason ?: @"drop peripheral", uuid, (long)p.state]);
    if (p.state == CBPeripheralStateConnected || p.state == CBPeripheralStateConnecting) {
        [_centralMgr cancelPeripheralConnection:p];
    }
    [self markPeripheralUnready:p.identifier];
    [_peripheralNames removeObjectForKey:uuid];
    [_peripherals removeObjectForKey:p.identifier];
}

- (void)scheduleRecoveryScan:(NSString *)reason {
    if (_recoveryScanScheduled || _centralMgr.state != CBManagerStatePoweredOn) return;
    _recoveryScanScheduled = YES;
    bd_ble_log([NSString stringWithFormat:@"recovery scan scheduled: %@", reason ?: @"unknown"]);
    dispatch_after(dispatch_time(DISPATCH_TIME_NOW, (int64_t)1 * NSEC_PER_SEC), _bleQueue, ^{
        if (_centralMgr.state == CBManagerStatePoweredOn) {
            [self startCentralScan];
        }
        dispatch_after(dispatch_time(DISPATCH_TIME_NOW, (int64_t)3 * NSEC_PER_SEC), _bleQueue, ^{
            [_centralMgr stopScan];
            _recoveryScanScheduled = NO;
            bd_ble_log(@"recovery scan stopped");
        });
    });
}

- (BOOL)isUsableAdvertisedName:(NSString *)name {
    return name && name.length > 0 && ![name isEqualToString:@"unknown"] && ![name isEqualToString:@"Mac"];
}

- (void)rememberAdvertisedName:(NSString *)name forPeripheral:(CBPeripheral *)p {
    if (![self isUsableAdvertisedName:name]) return;
    _peripheralNames[p.identifier.UUIDString] = name;
}

- (NSString *)fallbackNameForPeripheral:(CBPeripheral *)p {
    NSString *uuid = p.identifier.UUIDString;
    NSString *knownName = _peripheralNames[uuid];
    if ([self isUsableAdvertisedName:knownName]) return knownName;
    NSString *peripheralName = p.name;
    if ([self isUsableAdvertisedName:peripheralName]) return peripheralName;
    NSString *shortID = uuid.length >= 8 ? [uuid substringToIndex:8] : uuid;
    return [NSString stringWithFormat:@"ble-%@", shortID];
}

- (BOOL)notifyPeer:(CBPeripheral *)p name:(NSString *)name context:(NSString *)context {
    if (!name || name.length == 0) return NO;
    NSString *uuid = p.identifier.UUIDString;
    NSString *lastName = _notifiedPeripheralNames[uuid];
    if (lastName && [lastName isEqualToString:name]) {
        [_readyPeripheralIDs addObject:uuid];
        return YES;
    }
    _notifiedPeripheralNames[uuid] = name;
    _peripheralNames[uuid] = name;
    [_readyPeripheralIDs addObject:p.identifier.UUIDString];
    bd_ble_log([NSString stringWithFormat:@"%@; using peer name: %@ (%@)", context ?: @"nickname unavailable", name, p.identifier.UUIDString]);
    go_ble_peer_found([name UTF8String], [p.identifier.UUIDString UTF8String]);
    return YES;
}

- (BOOL)notifyPeerFromBestKnownName:(CBPeripheral *)p context:(NSString *)context {
    return [self notifyPeer:p name:[self fallbackNameForPeripheral:p] context:context];
}

- (void)scheduleConnectTimeoutForPeripheral:(CBPeripheral *)p name:(NSString *)name {
    if (!p) return;
    NSUUID *identifier = p.identifier;
    NSString *uuid = identifier.UUIDString;
    NSString *label = name ?: p.name ?: @"unknown";
    dispatch_after(dispatch_time(DISPATCH_TIME_NOW, (int64_t)8 * NSEC_PER_SEC), _bleQueue, ^{
        if (![_connectingPeripheralIDs containsObject:uuid] ||
            [_readyPeripheralIDs containsObject:uuid]) {
            return;
        }
        CBPeripheral *current = _peripherals[identifier];
        if (!current) return;
        if (current.state == CBPeripheralStateConnecting) {
            bd_ble_log([NSString stringWithFormat:@"connect timeout; waiting for OS callback: %@ (%@)", label, uuid]);
            dispatch_after(dispatch_time(DISPATCH_TIME_NOW, (int64_t)4 * NSEC_PER_SEC), _bleQueue, ^{
                if (![_connectingPeripheralIDs containsObject:uuid] ||
                    [_readyPeripheralIDs containsObject:uuid]) {
                    return;
                }
                CBPeripheral *stale = _peripherals[identifier];
                if (stale && stale.state == CBPeripheralStateConnecting) {
                    [self dropPeripheral:stale reason:@"connect timeout expired"];
                    [self scheduleRecoveryScan:@"connect timeout"];
                }
            });
        } else if (current.state == CBPeripheralStateConnected) {
            [_connectingPeripheralIDs removeObject:uuid];
            [current discoverServices:@[svcUUID()]];
        } else {
            [self markPeripheralUnready:identifier];
            [_peripheralNames removeObjectForKey:uuid];
            [_peripherals removeObjectForKey:identifier];
        }
    });
}

- (void)connectPeripheral:(CBPeripheral *)p name:(NSString *)name central:(CBCentralManager *)cm {
    if (!p) return;
    NSString *uuid = p.identifier.UUIDString;
    if ([_connectingPeripheralIDs containsObject:uuid]) return;
    [_connectingPeripheralIDs addObject:uuid];
    bd_ble_log([NSString stringWithFormat:@"connect requested: %@ (%@)", name ?: p.name ?: @"unknown", uuid]);
    [cm connectPeripheral:p options:nil];
    [self scheduleConnectTimeoutForPeripheral:p name:name];
}

- (void)centralManager:(CBCentralManager *)cm
 didDiscoverPeripheral:(CBPeripheral *)p
     advertisementData:(NSDictionary *)ad
                  RSSI:(NSNumber *)RSSI {
    CBPeripheral *existing = _peripherals[p.identifier];
    if (existing) {
        NSString *knownName = ad[CBAdvertisementDataLocalNameKey] ?: existing.name ?: @"unknown";
        [self rememberAdvertisedName:knownName forPeripheral:existing];
        return;
    }
    _peripherals[p.identifier] = p;
    NSString *name = ad[CBAdvertisementDataLocalNameKey] ?: p.name ?: @"unknown";
    [self rememberAdvertisedName:name forPeripheral:p];
    bd_ble_log([NSString stringWithFormat:@"discovered peripheral: %@ (%@)", name, p.identifier.UUIDString]);
    [self connectPeripheral:p name:name central:cm];
}

- (void)centralManager:(CBCentralManager *)cm didConnectPeripheral:(CBPeripheral *)p {
    [_connectingPeripheralIDs removeObject:p.identifier.UUIDString];
    [_connectRetryCounts removeObjectForKey:p.identifier.UUIDString];
    bd_ble_log([NSString stringWithFormat:@"connected peripheral: %@", p.identifier.UUIDString]);
    p.delegate = self;
    [p discoverServices:@[svcUUID()]];
}

- (void)centralManager:(CBCentralManager *)cm didFailToConnectPeripheral:(CBPeripheral *)p error:(NSError *)e {
    NSString *uuid = p.identifier.UUIDString;
    bd_ble_log([NSString stringWithFormat:@"connect failed: %@ %@", uuid, e.localizedDescription ?: @""]);
    NSInteger attempts = [_connectRetryCounts[uuid] integerValue];
    if (attempts < 1) {
        _connectRetryCounts[uuid] = @(attempts + 1);
        [_connectingPeripheralIDs removeObject:uuid];
        bd_ble_log([NSString stringWithFormat:@"connect retry scheduled: %@ attempt=%ld", uuid, (long)(attempts + 1)]);
        dispatch_after(dispatch_time(DISPATCH_TIME_NOW, (int64_t)1 * NSEC_PER_SEC), _bleQueue, ^{
            if ([_readyPeripheralIDs containsObject:uuid]) return;
            CBPeripheral *current = _peripherals[p.identifier] ?: p;
            if (current.state != CBPeripheralStateDisconnected) return;
            _peripherals[p.identifier] = current;
            NSString *name = _peripheralNames[uuid] ?: current.name ?: @"unknown";
            [self connectPeripheral:current name:name central:cm];
        });
        return;
    }
    [self dropPeripheral:p reason:@"connect failed"];
}

// Peripheral disconnected (out of range, BLE drop, peer app exit, etc.).
// Drop reassembly state so a future reconnect starts clean — otherwise any
// partial message buffered here corrupts the next message under the same key.
- (void)centralManager:(CBCentralManager *)cm didDisconnectPeripheral:(CBPeripheral *)p error:(NSError *)e {
    [self markPeripheralUnready:p.identifier];
    [_peripherals        removeObjectForKey:p.identifier];
}

- (void)peripheral:(CBPeripheral *)p didDiscoverServices:(NSError *)e {
    NSString *uuid = p.identifier.UUIDString;
    if (e) {
        bd_ble_log([NSString stringWithFormat:@"service discovery failed: %@ %@", p.identifier.UUIDString, e.localizedDescription ?: @"no services"]);
        [self dropPeripheral:p reason:@"service discovery failed"];
        [self scheduleRecoveryScan:@"service discovery failed"];
        return;
    }
    if (p.services.count == 0) {
        NSInteger attempts = [_serviceDiscoveryRetryCounts[uuid] integerValue];
        if (attempts < 1 && p.state == CBPeripheralStateConnected) {
            _serviceDiscoveryRetryCounts[uuid] = @(attempts + 1);
            bd_ble_log([NSString stringWithFormat:@"service discovery empty; retry scheduled: %@ attempt=%ld", uuid, (long)(attempts + 1)]);
            dispatch_after(dispatch_time(DISPATCH_TIME_NOW, (int64_t)1 * NSEC_PER_SEC), _bleQueue, ^{
                CBPeripheral *current = _peripherals[p.identifier];
                if (current.state == CBPeripheralStateConnected) {
                    [current discoverServices:@[svcUUID()]];
                }
            });
            return;
        }
        bd_ble_log([NSString stringWithFormat:@"service discovery failed: %@ no services", uuid]);
        [self dropPeripheral:p reason:@"service discovery failed"];
        [self scheduleRecoveryScan:@"service discovery empty"];
        return;
    }
    [_serviceDiscoveryRetryCounts removeObjectForKey:uuid];
    for (CBService *s in p.services)
        [p discoverCharacteristics:@[nickUUID(), sdpUUID(), dataUUID()] forService:s];
}

- (void)peripheral:(CBPeripheral *)p
didDiscoverCharacteristicsForService:(CBService *)s
             error:(NSError *)e {
    if (e || s.characteristics.count == 0) {
        bd_ble_log([NSString stringWithFormat:@"characteristic discovery failed: %@ %@", p.identifier.UUIDString, e.localizedDescription ?: @"no characteristics"]);
        [self dropPeripheral:p reason:@"characteristic discovery failed"];
        return;
    }
    for (CBCharacteristic *c in s.characteristics) {
        if ([c.UUID isEqual:nickUUID()])  [p readValueForCharacteristic:c];
        if ([c.UUID isEqual:sdpUUID()])   [p setNotifyValue:YES forCharacteristic:c];
        if ([c.UUID isEqual:dataUUID()])  [p setNotifyValue:YES forCharacteristic:c];
    }
    [self notifyPeerFromBestKnownName:p context:@"characteristics ready"];
}

- (void)peripheral:(CBPeripheral *)p
didUpdateValueForCharacteristic:(CBCharacteristic *)c
             error:(NSError *)e {
    if (e || !c.value) {
        if ([c.UUID isEqual:nickUUID()]) {
            [self notifyPeerFromBestKnownName:p context:[NSString stringWithFormat:@"nickname read failed: %@", e.localizedDescription ?: @"empty value"]];
        }
        return;
    }

    if ([c.UUID isEqual:nickUUID()]) {
        NSString *nick = [[NSString alloc] initWithData:c.value encoding:NSUTF8StringEncoding];
        if (!nick || nick.length == 0) {
            [self notifyPeerFromBestKnownName:p context:@"nickname read empty"];
            return;
        }
        [self notifyPeer:p name:nick context:@"nickname read"];
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
    if (total == 0) return;

    // idx==0 is the canonical "new message starts" signal — always reset the
    // buffer so a previous interrupted/dropped message cannot leak stale bytes
    // into the new one. Without this, a BLE disconnect mid-message corrupts
    // every subsequent message reassembled under the same key.
    if (idx == 0) {
        bufs[key] = [NSMutableData new];
    } else if (!bufs[key]) {
        // Mid-message chunk arrived without a prior idx==0 — drop it.
        return;
    }
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

    // See handleChunk: idx==0 must reset the buffer to prevent stale bytes
    // from a previously interrupted message corrupting the next one.
    if (idx == 0) {
        bufs[key] = [NSMutableData new];
    } else if (!bufs[key]) {
        return;
    }
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

void ble_scan_for(int seconds) {
    if (!gBLE) return;
    int secs = seconds <= 0 ? 5 : seconds;
    dispatch_async(gBLE.bleQueue, ^{
        [gBLE startCentralScan];
        dispatch_after(dispatch_time(DISPATCH_TIME_NOW, (int64_t)secs * NSEC_PER_SEC), gBLE.bleQueue, ^{
            [gBLE.centralMgr stopScan];
        });
    });
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
