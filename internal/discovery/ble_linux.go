//go:build linux

package discovery

import (
	"context"
	"fmt"
	"sync"
	"time"

	"tinygo.org/x/bluetooth"
)

var (
	linuxDataMu      sync.Mutex
	linuxDataCb      func(peerUUID string, data []byte)
	linuxDataCharsMu sync.RWMutex
	linuxDataChars   = map[string]bluetooth.DeviceCharacteristic{}
	linuxAssembler   = NewChunkAssembler()
	linuxAssemblerMu sync.Mutex
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
		time.Sleep(10 * time.Millisecond)
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
			go connectAndSubscribe(ctx, adapter, d, peerAddr, mgr)
		})
	}()
	<-ctx.Done()
	return nil
}

func connectAndSubscribe(ctx context.Context, adapter *bluetooth.Adapter, d bluetooth.ScanResult, peerAddr string, mgr *Manager) {
	linuxDataCharsMu.Lock()
	_, already := linuxDataChars[peerAddr]
	if !already {
		linuxDataChars[peerAddr] = bluetooth.DeviceCharacteristic{} // sentinel: connection in progress
	}
	linuxDataCharsMu.Unlock()
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
		linuxDataCharsMu.Lock()
		delete(linuxDataChars, peerAddr)
		linuxDataCharsMu.Unlock()
		linuxAssemblerMu.Lock()
		linuxAssembler.Reset(peerAddr)
		linuxAssemblerMu.Unlock()
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
			n, err := c.Read(buf)
			if err == nil && n > 0 {
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

	if err := dataChar.EnableNotifications(func(buf []byte) {
		linuxAssemblerMu.Lock()
		assembled, done := linuxAssembler.Feed(peerAddr, buf)
		linuxAssemblerMu.Unlock()
		if !done {
			return
		}
		linuxDataMu.Lock()
		cb := linuxDataCb
		linuxDataMu.Unlock()
		if cb != nil {
			cb(peerAddr, assembled)
		}
	}); err != nil {
		linuxDataCharsMu.Lock()
		delete(linuxDataChars, peerAddr)
		linuxDataCharsMu.Unlock()
		return
	}
}

func extractBLENickname(data []bluetooth.ManufacturerDataElement) string {
	for _, d := range data {
		if d.CompanyID == 0xFFFF && len(d.Data) > 0 {
			return string(d.Data)
		}
	}
	return "unknown"
}
