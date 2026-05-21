//go:build !darwin && !windows

package core

import (
	"context"

	"github.com/hsleedevelop/bdpeer/internal/discovery"
	"github.com/hsleedevelop/bdpeer/internal/transport"
)

// startBLEWithWebRTC on non-darwin wires BLE data transport as a flat data
// channel (no WebRTC upgrade). Peers are lazily registered on first inbound data.
func (s *Service) startBLEWithWebRTC(ctx context.Context, mgr *discovery.Manager) {
	s.bleT.SetHandler(s.onInboundStream)
	discovery.SetBLEDataCallback(func(peerUUID string, data []byte) {
		peerKey := transport.PeerID("ble-" + peerUUID)
		if _, ok := s.registry.Lookup(peerKey); !ok {
			s.bleT.Attach(peerKey, func(d []byte) {
				discovery.BLECentralSendData(peerUUID, d)
			})
			s.registry.Register(peerKey, s.bleT)
		}
		s.bleT.InboundData(peerKey, data)
	})
	if err := discovery.StartBLE(ctx, s.cfg.Nickname, mgr); err != nil {
		s.log("    ✗ BLE 실패: " + err.Error())
	}
}
