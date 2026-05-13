//go:build !ble

package discovery

import "context"

// StartBLE is a no-op when compiled without -tags ble.
func StartBLE(ctx context.Context, nickname string, mgr *Manager) error {
	return nil
}
