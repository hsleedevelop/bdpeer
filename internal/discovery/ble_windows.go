//go:build windows

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

func SetBLECallbacks(_ func(string, string), _ func(string, string, bool), _ func(string)) {}
func BLEPeripheralSendSDP(_ string)          {}
func BLECentralSendSDP(_ string, _ string)   {}
func SetBLEDataCallback(_ func(string, []byte)) {}
func BLEPeripheralSendDataTo(_ string, _ []byte) {}
func BLECentralSendData(_ string, _ []byte)      {}

func extractBLENickname(data []bluetooth.ManufacturerDataElement) string {
	for _, d := range data {
		if d.CompanyID == 0xFFFF && len(d.Data) > 0 {
			return string(d.Data)
		}
	}
	return "unknown"
}
