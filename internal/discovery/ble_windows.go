//go:build windows

package discovery

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"

	"tinygo.org/x/bluetooth"
)

var (
	windowsDataMu sync.Mutex
	windowsDataCb func(peerUUID string, data []byte)

	windowsLogMu sync.Mutex
	windowsLogCb func(string)

	windowsCallbacksMu sync.Mutex
	windowsPeerFoundCb func(nickname, peerUUID string)

	windowsSessionID string

	windowsDataCharsMu  sync.RWMutex
	windowsDataChars    = map[string]bluetooth.DeviceCharacteristic{}
	windowsConnectingMu sync.Mutex
	windowsConnecting   = map[string]struct{}{}
	windowsBackoff      = newConnectBackoffTable()

	windowsPeripheralMu        sync.Mutex
	windowsPeripheralDataChar  bluetooth.Characteristic
	windowsPeripheralAssembler = NewSessionChunkAssembler()
	windowsCentralAssembler    = NewSessionChunkAssembler()
	windowsCentralAssemblerMu  sync.Mutex

	windowsSuppressMu          sync.Mutex
	windowsSuppressLocalWrites int
)

var (
	bleServiceUUID               = bluetooth.NewUUID([16]byte{0xBD, 0x9E, 0x00, 0x01, 0xF0, 0xF0, 0x10, 0x00, 0x80, 0x00, 0x00, 0x80, 0x5F, 0x9B, 0x34, 0xFB})
	bleNickCharUUID              = bluetooth.NewUUID([16]byte{0xBD, 0x9E, 0x00, 0x02, 0xF0, 0xF0, 0x10, 0x00, 0x80, 0x00, 0x00, 0x80, 0x5F, 0x9B, 0x34, 0xFB})
	bleDataCharUUID              = bluetooth.NewUUID([16]byte{0xBD, 0x9E, 0x00, 0x04, 0xF0, 0xF0, 0x10, 0x00, 0x80, 0x00, 0x00, 0x80, 0x5F, 0x9B, 0x34, 0xFB})
	bleSessCharUUID              = bluetooth.NewUUID([16]byte{0xBD, 0x9E, 0x00, 0x05, 0xF0, 0xF0, 0x10, 0x00, 0x80, 0x00, 0x00, 0x80, 0x5F, 0x9B, 0x34, 0xFB})
	windowsConnectFailureBackoff = 2 * time.Minute
)

func StartBLE(ctx context.Context, nickname string, mgr *Manager) error {
	adapter := bluetooth.DefaultAdapter
	if err := adapter.Enable(); err != nil {
		return fmt.Errorf("BLE enable (ensure Bluetooth is on): %w", err)
	}
	logWindowsBLE("adapter enabled")

	windowsSessionID = newWindowsSessionID()
	logWindowsBLE("session ready: %s", windowsSessionID)

	if err := addWindowsService(adapter, nickname); err != nil {
		return err
	}
	if err := startWindowsAdvertisement(ctx, adapter, nickname); err != nil {
		return err
	}
	go scanWindowsBLE(ctx, adapter, nickname, mgr)

	<-ctx.Done()
	return nil
}

func startWindowsAdvertisement(ctx context.Context, adapter *bluetooth.Adapter, nickname string) error {
	adv := adapter.DefaultAdvertisement()
	if err := adv.Configure(bluetooth.AdvertisementOptions{
		ManufacturerData: []bluetooth.ManufacturerDataElement{
			{CompanyID: 0xFFFF, Data: []byte(nickname)},
		},
	}); err != nil {
		return fmt.Errorf("BLE advertisement configure: %w", err)
	}
	if err := adv.Start(); err != nil {
		return fmt.Errorf("BLE advertisement start: %w", err)
	}
	logWindowsBLE("advertising started")
	go func() {
		<-ctx.Done()
		_ = adv.Stop()
	}()
	return nil
}

func scanWindowsBLE(ctx context.Context, adapter *bluetooth.Adapter, nickname string, mgr *Manager) {
	logWindowsBLE("auto scan started")
	for ctx.Err() == nil {
		found, err := scanWindowsBLEWindow(ctx, adapter, nickname, 5*time.Second)
		if err != nil && ctx.Err() == nil {
			logWindowsBLE("auto scan failed: %v", err)
			time.Sleep(time.Second)
			continue
		}
		for _, d := range found {
			if ctx.Err() != nil {
				return
			}
			peerAddr := d.Address.String()
			if !beginWindowsConnect(peerAddr) {
				continue
			}
			connectAndSubscribeWindows(ctx, adapter, d, peerAddr, nickname, mgr)
		}
	}
}

func scanWindowsBLEWindow(ctx context.Context, adapter *bluetooth.Adapter, nickname string, duration time.Duration) ([]bluetooth.ScanResult, error) {
	scanCtx, cancel := context.WithTimeout(ctx, duration)
	defer cancel()

	candidates := make(map[string]bluetooth.ScanResult)
	seen := make(map[string]struct{})
	var candidatesMu sync.Mutex
	scanDone := make(chan error, 1)

	go func() {
		scanDone <- adapter.Scan(func(a *bluetooth.Adapter, d bluetooth.ScanResult) {
			select {
			case <-scanCtx.Done():
				_ = a.StopScan()
				return
			default:
			}
			if !isBDPeerAdvertisement(d) {
				return
			}
			details := windowsAdvertisementDetails(d)
			if details.manufacturerNick != "unknown" && details.manufacturerNick == nickname {
				logWindowsBLE("scan skipped self advertisement: manufacturerNick=%q localName=%q hasServiceUUID=%v addressRandom=%v rssi=%d", details.manufacturerNick, details.localName, details.hasServiceUUID, details.addressRandom, details.rssi)
				return
			}
			peerAddr := d.Address.String()
			if _, active := windowsBackoff.Active(peerAddr, time.Now()); active {
				return
			}
			candidatesMu.Lock()
			if _, ok := seen[peerAddr]; !ok {
				logWindowsBLE("scan candidate: addr=%s hasServiceUUID=%v addressRandom=%v manufacturerNick=%q localName=%q rssi=%d", peerAddr, details.hasServiceUUID, details.addressRandom, details.manufacturerNick, details.localName, details.rssi)
				seen[peerAddr] = struct{}{}
			}
			if !isWindowsBLEConnectCandidate(details) {
				candidatesMu.Unlock()
				return
			}
			if _, ok := candidates[peerAddr]; !ok {
				logWindowsBLE("scan connect candidate: addr=%s hasServiceUUID=%v addressRandom=%v manufacturerNick=%q localName=%q rssi=%d", peerAddr, details.hasServiceUUID, details.addressRandom, details.manufacturerNick, details.localName, details.rssi)
			}
			candidates[peerAddr] = d
			candidatesMu.Unlock()
		})
	}()

	select {
	case <-scanCtx.Done():
		_ = adapter.StopScan()
	case err := <-scanDone:
		return windowsScanResults(candidates, &candidatesMu), err
	}

	select {
	case err := <-scanDone:
		return windowsScanResults(candidates, &candidatesMu), err
	case <-time.After(2 * time.Second):
		return nil, fmt.Errorf("scan stop timeout")
	}
}

func windowsScanResults(candidates map[string]bluetooth.ScanResult, candidatesMu *sync.Mutex) []bluetooth.ScanResult {
	candidatesMu.Lock()
	defer candidatesMu.Unlock()
	found := make([]bluetooth.ScanResult, 0, len(candidates))
	for _, d := range candidates {
		found = append(found, d)
	}
	return found
}

func SetBLECallbacks(onPeer func(string, string), _ func(string, string, bool), _ func(string)) {
	windowsCallbacksMu.Lock()
	windowsPeerFoundCb = onPeer
	windowsCallbacksMu.Unlock()
}

func BLEPeripheralSendSDP(_ string)        {}
func BLECentralSendSDP(_ string, _ string) {}

func SetBLEDataCallback(onData func(peerUUID string, data []byte)) {
	windowsDataMu.Lock()
	windowsDataCb = onData
	windowsDataMu.Unlock()
}

func SetBLELogCallback(onLog func(string)) {
	windowsLogMu.Lock()
	windowsLogCb = onLog
	windowsLogMu.Unlock()
}

func beginWindowsConnect(peerAddr string) bool {
	windowsConnectingMu.Lock()
	defer windowsConnectingMu.Unlock()
	if _, already := windowsConnecting[peerAddr]; already {
		return false
	}
	windowsConnecting[peerAddr] = struct{}{}
	return true
}

func BLEPeripheralSendDataTo(peerSessionID string, data []byte) {
	windowsPeripheralMu.Lock()
	char := windowsPeripheralDataChar
	windowsPeripheralMu.Unlock()
	for _, chunk := range MakeSessionDataChunks(windowsSessionID, peerSessionID, data) {
		suppressNextWindowsLocalWrite()
		char.Write(chunk)
		time.Sleep(10 * time.Millisecond)
	}
}

func BLECentralSendData(peerSessionID string, data []byte) {
	windowsDataCharsMu.RLock()
	char, ok := windowsDataChars[peerSessionID]
	windowsDataCharsMu.RUnlock()
	if !ok {
		BLEPeripheralSendDataTo(peerSessionID, data)
		return
	}
	for _, chunk := range MakeSessionDataChunks(windowsSessionID, peerSessionID, data) {
		char.WriteWithoutResponse(chunk)
		time.Sleep(10 * time.Millisecond)
	}
}

func addWindowsService(adapter *bluetooth.Adapter, nickname string) error {
	var dataChar bluetooth.Characteristic
	err := adapter.AddService(&bluetooth.Service{
		UUID: bleServiceUUID,
		Characteristics: []bluetooth.CharacteristicConfig{
			{
				UUID:  bleNickCharUUID,
				Value: []byte(nickname),
				Flags: bluetooth.CharacteristicReadPermission,
			},
			{
				UUID:  bleSessCharUUID,
				Value: []byte(windowsSessionID),
				Flags: bluetooth.CharacteristicReadPermission,
			},
			{
				Handle: &dataChar,
				UUID:   bleDataCharUUID,
				Flags: bluetooth.CharacteristicWriteWithoutResponsePermission |
					bluetooth.CharacteristicWritePermission |
					bluetooth.CharacteristicNotifyPermission,
				WriteEvent: func(_ bluetooth.Connection, _ int, value []byte) {
					if consumeWindowsLocalWriteSuppression() {
						return
					}
					windowsPeripheralMu.Lock()
					fromSession, assembled, done := windowsPeripheralAssembler.Feed(windowsSessionID, value)
					windowsPeripheralMu.Unlock()
					if done {
						emitWindowsData(fromSession, assembled)
					}
				},
			},
		},
	})
	if err != nil {
		return fmt.Errorf("BLE GATT service start: %w", err)
	}
	windowsPeripheralMu.Lock()
	windowsPeripheralDataChar = dataChar
	windowsPeripheralMu.Unlock()
	logWindowsBLE("GATT service started")
	return nil
}

func connectAndSubscribeWindows(ctx context.Context, adapter *bluetooth.Adapter, d bluetooth.ScanResult, peerAddr, localNickname string, mgr *Manager) {
	var remoteSession string
	cleanup := func() {
		windowsConnectingMu.Lock()
		delete(windowsConnecting, peerAddr)
		windowsConnectingMu.Unlock()
		if remoteSession == "" {
			return
		}
		windowsDataCharsMu.Lock()
		delete(windowsDataChars, remoteSession)
		windowsDataCharsMu.Unlock()
		windowsCentralAssemblerMu.Lock()
		windowsCentralAssembler.Reset(remoteSession)
		windowsCentralAssemblerMu.Unlock()
		windowsPeripheralMu.Lock()
		windowsPeripheralAssembler.Reset(remoteSession)
		windowsPeripheralMu.Unlock()
	}

	details := windowsAdvertisementDetails(d)
	logWindowsBLE("connect attempt: addr=%s hasServiceUUID=%v addressRandom=%v manufacturerNick=%q localName=%q rssi=%d", peerAddr, details.hasServiceUUID, details.addressRandom, details.manufacturerNick, details.localName, details.rssi)
	dev, err := adapter.Connect(d.Address, bluetooth.ConnectionParams{})
	if err != nil {
		logWindowsBLE("connect failed: addr=%s err=%v", peerAddr, err)
		if isWindowsAddressNotFound(err) {
			until := windowsBackoff.Mark(peerAddr, time.Now(), windowsConnectFailureBackoff)
			logWindowsBLE("connect backoff: addr=%s retryAfter=%s reason=address-not-found", peerAddr, time.Until(until).Round(time.Second))
		}
		cleanup()
		return
	}
	windowsBackoff.Clear(peerAddr)
	logWindowsBLE("connected: addr=%s", peerAddr)
	go func() {
		<-ctx.Done()
		dev.Disconnect()
		cleanup()
	}()

	srvcs, err := dev.DiscoverServices([]bluetooth.UUID{bleServiceUUID})
	if err != nil || len(srvcs) == 0 {
		logWindowsBLE("service discovery failed: addr=%s services=%d err=%v", peerAddr, len(srvcs), err)
		cleanup()
		return
	}
	chars, err := srvcs[0].DiscoverCharacteristics(nil)
	if err != nil {
		logWindowsBLE("characteristic discovery failed: addr=%s err=%v", peerAddr, err)
		cleanup()
		return
	}

	nick := extractBLENickname(d.ManufacturerData())
	if nick == "unknown" {
		nick = d.LocalName()
	}
	var dataChar bluetooth.DeviceCharacteristic
	for _, c := range chars {
		switch c.UUID() {
		case bleNickCharUUID:
			buf := make([]byte, 64)
			n, err := c.Read(buf)
			if err == nil && n > 0 {
				nick = string(buf[:n])
			}
		case bleSessCharUUID:
			buf := make([]byte, 64)
			n, err := c.Read(buf)
			if err == nil && n > 0 {
				remoteSession = string(buf[:n])
			}
		case bleDataCharUUID:
			dataChar = c
		}
	}

	if nick == "" || nick == "unknown" || nick == localNickname ||
		remoteSession == "" || remoteSession == windowsSessionID ||
		dataChar.UUID() == (bluetooth.UUID{}) {
		logWindowsBLE("peer rejected: addr=%s nick=%q session=%q dataChar=%v", peerAddr, nick, remoteSession, dataChar.UUID() != (bluetooth.UUID{}))
		cleanup()
		_ = dev.Disconnect()
		return
	}

	windowsDataCharsMu.Lock()
	windowsDataChars[remoteSession] = dataChar
	windowsDataCharsMu.Unlock()
	notifyWindowsPeerFound(mgr, nick, remoteSession)
	logWindowsBLE("peer ready: nick=%s session=%s", nick, remoteSession)

	if err := dataChar.EnableNotifications(func(buf []byte) {
		windowsCentralAssemblerMu.Lock()
		fromSession, assembled, done := windowsCentralAssembler.Feed(windowsSessionID, buf)
		windowsCentralAssemblerMu.Unlock()
		if done {
			emitWindowsData(fromSession, assembled)
		}
	}); err != nil {
		logWindowsBLE("notifications failed: session=%s err=%v", remoteSession, err)
		cleanup()
		return
	}
	logWindowsBLE("notifications enabled: session=%s", remoteSession)
}

func isBDPeerAdvertisement(d bluetooth.ScanResult) bool {
	details := windowsAdvertisementDetails(d)
	return isWindowsBLEConnectCandidate(details)
}

func isWindowsBLEConnectCandidate(details windowsScanDetails) bool {
	return details.hasServiceUUID || details.manufacturerNick != unknownBLENickname
}

type windowsScanDetails struct {
	hasServiceUUID   bool
	addressRandom    bool
	manufacturerNick string
	localName        string
	rssi             int16
}

func windowsAdvertisementDetails(d bluetooth.ScanResult) windowsScanDetails {
	details := windowsScanDetails{
		manufacturerNick: "unknown",
		addressRandom:    d.Address.IsRandom(),
		rssi:             d.RSSI,
	}
	if d.AdvertisementPayload == nil {
		return details
	}
	details.hasServiceUUID = d.HasServiceUUID(bleServiceUUID)
	details.manufacturerNick = extractBLENickname(d.ManufacturerData())
	details.localName = d.LocalName()
	return details
}

func notifyWindowsPeerFound(mgr *Manager, nickname, peerUUID string) {
	windowsCallbacksMu.Lock()
	cb := windowsPeerFoundCb
	windowsCallbacksMu.Unlock()
	if cb != nil {
		cb(nickname, peerUUID)
		return
	}
	mgr.Notify(DiscoveredPeer{Nickname: nickname, Addr: peerUUID, Source: "ble"})
}

func emitWindowsData(peerUUID string, data []byte) {
	windowsDataMu.Lock()
	cb := windowsDataCb
	windowsDataMu.Unlock()
	if cb != nil {
		cb(peerUUID, data)
	}
}

func logWindowsBLE(format string, args ...any) {
	windowsLogMu.Lock()
	cb := windowsLogCb
	windowsLogMu.Unlock()
	if cb != nil {
		cb(fmt.Sprintf(format, args...))
	}
}

func isWindowsAddressNotFound(err error) bool {
	return err != nil && strings.Contains(err.Error(), "device with the given address was not found")
}

func suppressNextWindowsLocalWrite() {
	windowsSuppressMu.Lock()
	windowsSuppressLocalWrites++
	windowsSuppressMu.Unlock()
}

func consumeWindowsLocalWriteSuppression() bool {
	windowsSuppressMu.Lock()
	defer windowsSuppressMu.Unlock()
	if windowsSuppressLocalWrites == 0 {
		return false
	}
	windowsSuppressLocalWrites--
	return true
}

func newWindowsSessionID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err == nil {
		return hex.EncodeToString(b[:])
	}
	return fmt.Sprintf("%016x", time.Now().UnixNano())
}

func extractBLENickname(data []bluetooth.ManufacturerDataElement) string {
	for _, d := range data {
		if d.CompanyID == 0xFFFF && len(d.Data) > 0 {
			if nick := normalizeBLENickname(d.Data); nick != unknownBLENickname {
				return nick
			}
		}
	}
	return unknownBLENickname
}
