//go:build windows

package discovery

import (
	"context"
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

	windowsDataCharsMu sync.RWMutex
	windowsDataChars   = map[string]bluetooth.DeviceCharacteristic{}
	windowsDefaultPeer string

	windowsPeripheralMu        sync.Mutex
	windowsPeripheralDataChar  bluetooth.Characteristic
	windowsPeripheralAssembler = NewChunkAssembler()
	windowsCentralAssembler    = NewChunkAssembler()
	windowsCentralAssemblerMu  sync.Mutex

	windowsSuppressMu          sync.Mutex
	windowsSuppressLocalWrites int
)

var (
	bleServiceUUID  = bluetooth.NewUUID([16]byte{0xBD, 0x9E, 0x00, 0x01, 0xF0, 0xF0, 0x10, 0x00, 0x80, 0x00, 0x00, 0x80, 0x5F, 0x9B, 0x34, 0xFB})
	bleNickCharUUID = bluetooth.NewUUID([16]byte{0xBD, 0x9E, 0x00, 0x02, 0xF0, 0xF0, 0x10, 0x00, 0x80, 0x00, 0x00, 0x80, 0x5F, 0x9B, 0x34, 0xFB})
	bleDataCharUUID = bluetooth.NewUUID([16]byte{0xBD, 0x9E, 0x00, 0x04, 0xF0, 0xF0, 0x10, 0x00, 0x80, 0x00, 0x00, 0x80, 0x5F, 0x9B, 0x34, 0xFB})
)

func StartBLE(ctx context.Context, nickname string, mgr *Manager) error {
	adapter := bluetooth.DefaultAdapter
	if err := adapter.Enable(); err != nil {
		return fmt.Errorf("BLE enable (ensure Bluetooth is on): %w", err)
	}

	if err := addWindowsService(adapter, nickname); err != nil {
		return err
	}

	go func() {
		_ = adapter.Scan(func(a *bluetooth.Adapter, d bluetooth.ScanResult) {
			select {
			case <-ctx.Done():
				a.StopScan()
				return
			default:
			}
			if !isBDPeerAdvertisement(d) {
				return
			}
			peerAddr := d.Address.String()
			go connectAndSubscribeWindows(ctx, adapter, d, peerAddr, nickname, mgr)
		})
	}()
	<-ctx.Done()
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

func BLEPeripheralSendDataTo(_ string, data []byte) {
	windowsPeripheralMu.Lock()
	char := windowsPeripheralDataChar
	windowsPeripheralMu.Unlock()
	for _, chunk := range MakeDataChunks(data) {
		suppressNextWindowsLocalWrite()
		char.Write(chunk)
		time.Sleep(10 * time.Millisecond)
	}
}

func BLECentralSendData(peripheralUUID string, data []byte) {
	windowsDataCharsMu.RLock()
	char, ok := windowsDataChars[peripheralUUID]
	windowsDataCharsMu.RUnlock()
	if !ok {
		return
	}
	for _, chunk := range MakeDataChunks(data) {
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
				Handle: &dataChar,
				UUID:   bleDataCharUUID,
				Flags: bluetooth.CharacteristicWriteWithoutResponsePermission |
					bluetooth.CharacteristicWritePermission |
					bluetooth.CharacteristicNotifyPermission,
				WriteEvent: func(_ bluetooth.Connection, _ int, value []byte) {
					if consumeWindowsLocalWriteSuppression() {
						return
					}
					peerKey := currentWindowsDefaultPeer()
					windowsPeripheralMu.Lock()
					assembled, done := windowsPeripheralAssembler.Feed(peerKey, value)
					windowsPeripheralMu.Unlock()
					if done {
						emitWindowsData(peerKey, assembled)
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
	windowsDataCharsMu.Lock()
	_, already := windowsDataChars[peerAddr]
	if !already {
		windowsDataChars[peerAddr] = bluetooth.DeviceCharacteristic{}
	}
	windowsDataCharsMu.Unlock()
	if already {
		return
	}

	cleanup := func() {
		windowsDataCharsMu.Lock()
		delete(windowsDataChars, peerAddr)
		if windowsDefaultPeer == peerAddr {
			windowsDefaultPeer = ""
		}
		windowsDataCharsMu.Unlock()
		windowsCentralAssemblerMu.Lock()
		windowsCentralAssembler.Reset(peerAddr)
		windowsCentralAssemblerMu.Unlock()
		windowsPeripheralMu.Lock()
		windowsPeripheralAssembler.Reset(peerAddr)
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
	chars, err := srvcs[0].DiscoverCharacteristics([]bluetooth.UUID{bleNickCharUUID, bleDataCharUUID})
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
		case bleDataCharUUID:
			dataChar = c
		}
	}

	if nick == "" || nick == "unknown" || nick == localNickname || dataChar.UUID() == (bluetooth.UUID{}) {
		cleanup()
		_ = dev.Disconnect()
		return
	}

	windowsDataCharsMu.Lock()
	windowsDataChars[peerAddr] = dataChar
	windowsDefaultPeer = peerAddr
	windowsDataCharsMu.Unlock()
	notifyWindowsPeerFound(mgr, nick, peerAddr)

	if err := dataChar.EnableNotifications(func(buf []byte) {
		windowsCentralAssemblerMu.Lock()
		assembled, done := windowsCentralAssembler.Feed(peerAddr, buf)
		windowsCentralAssemblerMu.Unlock()
		if done {
			emitWindowsData(peerAddr, assembled)
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

func currentWindowsDefaultPeer() string {
	windowsDataCharsMu.RLock()
	peer := windowsDefaultPeer
	windowsDataCharsMu.RUnlock()
	if peer == "" {
		return "peripheral"
	}
	return peer
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

func extractBLENickname(data []bluetooth.ManufacturerDataElement) string {
	for _, d := range data {
		if d.CompanyID == 0xFFFF && len(d.Data) > 0 {
			return string(d.Data)
		}
	}
	return "unknown"
}
