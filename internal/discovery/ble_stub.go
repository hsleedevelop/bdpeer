//go:build !darwin && !windows && !linux

package discovery

import "context"

// StartBLE is a no-op when compiled without -tags ble.
func StartBLE(ctx context.Context, nickname string, mgr *Manager) error {
	return nil
}

func SetBLECallbacks(_ func(string, string), _ func(string, string, bool), _ func(string)) {}
func BLEPeripheralSendSDP(_ string)                                                        {}
func BLECentralSendSDP(_ string, _ string)                                                 {}
func SetBLEDataCallback(_ func(string, []byte))                                            {}
func BLEPeripheralSendDataTo(_ string, _ []byte)                                           {}
func BLECentralSendData(_ string, _ []byte)                                                {}
