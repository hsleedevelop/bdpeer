//go:build windows

package discovery

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"tinygo.org/x/bluetooth"
)

var (
	windowsDataMu sync.Mutex
	windowsDataCb func(peerUUID string, data []byte)

	windowsCallbacksMu sync.Mutex
	windowsPeerFoundCb func(nickname, peerUUID string)

	windowsSearchMu       sync.Mutex
	windowsSearchMgr      *Manager
	windowsSearchNickname string
	windowsScanning       bool

	windowsSessionID string

	windowsDataCharsMu  sync.RWMutex
	windowsDataChars    = map[string]bluetooth.DeviceCharacteristic{}
	windowsConnectingMu sync.Mutex
	windowsConnecting   = map[string]struct{}{}

	windowsPeripheralMu        sync.Mutex
	windowsPeripheralDataChar  bluetooth.Characteristic
	windowsPeripheralAssembler = NewSessionChunkAssembler()
	windowsCentralAssembler    = NewSessionChunkAssembler()
	windowsCentralAssemblerMu  sync.Mutex

	windowsSuppressMu          sync.Mutex
	windowsSuppressLocalWrites int
)

var (
	bleServiceUUID  = bluetooth.NewUUID([16]byte{0xBD, 0x9E, 0x00, 0x01, 0xF0, 0xF0, 0x10, 0x00, 0x80, 0x00, 0x00, 0x80, 0x5F, 0x9B, 0x34, 0xFB})
	bleNickCharUUID = bluetooth.NewUUID([16]byte{0xBD, 0x9E, 0x00, 0x02, 0xF0, 0xF0, 0x10, 0x00, 0x80, 0x00, 0x00, 0x80, 0x5F, 0x9B, 0x34, 0xFB})
	bleDataCharUUID = bluetooth.NewUUID([16]byte{0xBD, 0x9E, 0x00, 0x04, 0xF0, 0xF0, 0x10, 0x00, 0x80, 0x00, 0x00, 0x80, 0x5F, 0x9B, 0x34, 0xFB})
	bleSessCharUUID = bluetooth.NewUUID([16]byte{0xBD, 0x9E, 0x00, 0x05, 0xF0, 0xF0, 0x10, 0x00, 0x80, 0x00, 0x00, 0x80, 0x5F, 0x9B, 0x34, 0xFB})
)

func StartBLE(ctx context.Context, nickname string, mgr *Manager) error {
	adapter := bluetooth.DefaultAdapter
	if err := adapter.Enable(); err != nil {
		return fmt.Errorf("BLE enable (ensure Bluetooth is on): %w", err)
	}
	windowsSearchMu.Lock()
	windowsSearchMgr = mgr
	windowsSearchNickname = nickname
	windowsSearchMu.Unlock()

	windowsSessionID = newWindowsSessionID()

	if err := addWindowsService(adapter, nickname); err != nil {
		return err
	}
	if err := startWindowsAdvertisement(ctx, adapter, nickname); err != nil {
		return err
	}

	<-ctx.Done()
	return nil
}

func SearchBLE(ctx context.Context, duration time.Duration) error {
	windowsSearchMu.Lock()
	if windowsScanning {
		windowsSearchMu.Unlock()
		return nil
	}
	windowsScanning = true
	nickname := windowsSearchNickname
	mgr := windowsSearchMgr
	windowsSearchMu.Unlock()
	defer func() {
		windowsSearchMu.Lock()
		windowsScanning = false
		windowsSearchMu.Unlock()
	}()

	if mgr == nil {
		return fmt.Errorf("BLE search requested before BLE start")
	}

	adapter := bluetooth.DefaultAdapter
	scanCtx, cancel := context.WithTimeout(ctx, duration)
	defer cancel()

	var found bluetooth.ScanResult
	foundPeer := false
	go func() {
		<-scanCtx.Done()
		_ = adapter.StopScan()
	}()
	_ = adapter.Scan(func(a *bluetooth.Adapter, d bluetooth.ScanResult) {
		select {
		case <-scanCtx.Done():
			_ = a.StopScan()
			return
		default:
		}
		if !isBDPeerAdvertisement(d) {
			return
		}
		peerAddr := d.Address.String()
		if !beginWindowsConnect(peerAddr) {
			return
		}
		found = d
		foundPeer = true
		_ = a.StopScan()
	})

	if foundPeer {
		go connectAndSubscribeWindows(ctx, adapter, found, found.Address.String(), nickname, mgr)
	}
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
	go func() {
		<-ctx.Done()
		_ = adv.Stop()
	}()
	return nil
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

	dev, err := adapter.Connect(d.Address, bluetooth.ConnectionParams{})
	if err != nil {
		cleanup()
		return
	}
	go func() {
		<-ctx.Done()
		dev.Disconnect()
		cleanup()
	}()

	srvcs, err := dev.DiscoverServices([]bluetooth.UUID{bleServiceUUID})
	if err != nil || len(srvcs) == 0 {
		cleanup()
		return
	}
	chars, err := srvcs[0].DiscoverCharacteristics(nil)
	if err != nil {
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
		cleanup()
		_ = dev.Disconnect()
		return
	}

	windowsDataCharsMu.Lock()
	windowsDataChars[remoteSession] = dataChar
	windowsDataCharsMu.Unlock()
	notifyWindowsPeerFound(mgr, nick, remoteSession)

	if err := dataChar.EnableNotifications(func(buf []byte) {
		windowsCentralAssemblerMu.Lock()
		fromSession, assembled, done := windowsCentralAssembler.Feed(windowsSessionID, buf)
		windowsCentralAssemblerMu.Unlock()
		if done {
			emitWindowsData(fromSession, assembled)
		}
	}); err != nil {
		cleanup()
		return
	}
}

func isBDPeerAdvertisement(d bluetooth.ScanResult) bool {
	if d.AdvertisementPayload == nil {
		return false
	}
	if d.HasServiceUUID(bleServiceUUID) {
		return true
	}
	return extractBLENickname(d.ManufacturerData()) != "unknown"
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
			return string(d.Data)
		}
	}
	return "unknown"
}
