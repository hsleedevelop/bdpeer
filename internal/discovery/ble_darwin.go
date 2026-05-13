//go:build ble && darwin

package discovery

import (
	"context"
	"fmt"

	"tinygo.org/x/bluetooth"
)

// StartBLE on darwin: scan-only (tinygo bluetooth does not support peripheral
// advertising on macOS). For full discovery, pair with another peer running
// on linux/windows or rely on other backends (mDNS/SSDP) on this host.
func StartBLE(ctx context.Context, nickname string, mgr *Manager) error {
	adapter := bluetooth.DefaultAdapter
	if err := adapter.Enable(); err != nil {
		return fmt.Errorf("BLE enable: %w", err)
	}

	go func() {
		_ = adapter.Scan(func(a *bluetooth.Adapter, device bluetooth.ScanResult) {
			select {
			case <-ctx.Done():
				a.StopScan()
				return
			default:
			}
			nick := extractBLENickname(device.ManufacturerData())
			if nick != "unknown" {
				mgr.Notify(DiscoveredPeer{
					Nickname: nick,
					Addr:     device.Address.String(),
					Source:   "ble",
				})
			}
		})
	}()

	<-ctx.Done()
	return nil
}

func extractBLENickname(data []bluetooth.ManufacturerDataElement) string {
	for _, d := range data {
		if d.CompanyID == 0xFFFF && len(d.Data) > 0 {
			return string(d.Data)
		}
	}
	return "unknown"
}
