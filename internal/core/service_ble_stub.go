//go:build !ble || !darwin

package core

import (
	"context"

	"github.com/hsleedevelop/bdpeer/internal/discovery"
)

// startBLEWithWebRTC is a no-op on non-darwin or non-ble builds.
func (s *Service) startBLEWithWebRTC(ctx context.Context, mgr *discovery.Manager) {
	if err := discovery.StartBLE(ctx, s.cfg.Nickname, mgr); err != nil {
		s.log("    ✗ BLE 실패: " + err.Error())
	}
}
