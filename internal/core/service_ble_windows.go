//go:build windows

package core

import (
	"context"

	"github.com/hsleedevelop/bdpeer/internal/discovery"
	"github.com/hsleedevelop/bdpeer/internal/proto"
	"github.com/hsleedevelop/bdpeer/internal/transfer"
	"github.com/hsleedevelop/bdpeer/internal/transport"
	"github.com/libp2p/go-libp2p/core/peer"
)

// startBLEWithWebRTC on Windows wires BLE as a direct GATT data transport.
// Windows does not perform the Darwin BLE->WebRTC upgrade path yet.
func (s *Service) startBLEWithWebRTC(ctx context.Context, mgr *discovery.Manager) {
	s.bleT.SetHandler(s.onInboundStream)
	discovery.SetBLELogCallback(func(msg string) {
		s.log("[ble] " + msg)
	})

	attachCentralPeer := func(peerUUID string) transport.PeerID {
		peerKey := transport.PeerID("ble-" + peerUUID)
		if _, ok := s.registry.Lookup(peerKey); ok {
			return peerKey
		}
		s.bleT.Attach(peerKey, func(data []byte) {
			discovery.BLECentralSendData(peerUUID, data)
		})
		s.registry.Register(peerKey, s.bleT)
		return peerKey
	}

	discovery.SetBLEDataCallback(func(peerUUID string, data []byte) {
		peerKey := attachCentralPeer(peerUUID)
		s.bleT.InboundData(peerKey, data)
	})

	discovery.SetBLECallbacks(
		func(nickname, peerUUID string) {
			peerKey := attachCentralPeer(peerUUID)
			mgr.Notify(discovery.DiscoveredPeer{
				ID:       peer.ID(peerKey),
				Nickname: nickname,
				Addr:     peerUUID,
				Source:   "ble",
			})
			go s.sendBLEHello(ctx, peerKey)
		},
		nil,
		nil,
	)

	if err := discovery.StartBLE(ctx, s.cfg.Nickname, mgr); err != nil {
		s.log("    ✗ BLE 실패: " + err.Error())
	}
}

// sendBLEHello sends a Hello frame to a peer over the BLE data transport.
func (s *Service) sendBLEHello(ctx context.Context, peerKey transport.PeerID) {
	stream, err := s.bleT.OpenStream(ctx, peerKey)
	if err != nil {
		return
	}
	defer stream.Close()
	_ = transfer.WriteFrame(stream, proto.Frame{Type: proto.FrameHello, From: s.cfg.Nickname})
}
