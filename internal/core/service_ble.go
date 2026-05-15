//go:build darwin

package core

import (
	"context"

	"github.com/hsleedevelop/bdpeer/internal/discovery"
	bnet "github.com/hsleedevelop/bdpeer/internal/net"
	"github.com/hsleedevelop/bdpeer/internal/proto"
	"github.com/hsleedevelop/bdpeer/internal/transfer"
	"github.com/hsleedevelop/bdpeer/internal/transport"
	"github.com/libp2p/go-libp2p/core/peer"
)

// startBLEWithWebRTC wires CoreBluetooth BLE discovery to the BLEWebRTCUpgrader,
// then starts BLE peripheral+central. When a WebRTC data channel is ready the
// peer is registered with the Manager and the bdpeer Hello frame is exchanged.
func (s *Service) startBLEWithWebRTC(ctx context.Context, mgr *discovery.Manager) {
	upgrader := bnet.NewBLEWebRTCUpgrader(s.cfg.Nickname)
	upgrader.OnLog = s.log
	upgrader.TURNServers = s.cfg.ICEServers()
	upgrader.SendOfferFn = discovery.BLECentralSendSDP
	upgrader.SendAnswerFn = discovery.BLEPeripheralSendSDP

	upgrader.OnConnected = func(peerUUID, peerNickname string, conn *bnet.WebRTCConn) {
		s.handleWebRTCConn(ctx, peerUUID, peerNickname, conn, mgr)
	}

	discovery.SetBLECallbacks(
		func(nickname, peerUUID string) {
			upgrader.OnBLEPeerFound(ctx, nickname, peerUUID)
		},
		func(peerUUID, sdp string, isOffer bool) {
			upgrader.OnSDPReceived(ctx, peerUUID, sdp, isOffer)
		},
		func(centralUUID string) {
			s.log("[BLE] central 구독: " + centralUUID)
		},
	)

	if err := discovery.StartBLE(ctx, s.cfg.Nickname, mgr); err != nil {
		s.log("    ✗ BLE 실패: " + err.Error())
	}
}

// handleWebRTCConn registers a newly connected WebRTC peer with the Manager
// and the transport registry. Inbound frames are delivered via the transport's
// reader goroutine (spawned by Attach) which calls s.onInboundStream.
func (s *Service) handleWebRTCConn(ctx context.Context, peerUUID, peerNickname string, conn *bnet.WebRTCConn, mgr *discovery.Manager) {
	peerID := peer.ID("ble-" + peerUUID)
	if peerNickname != "" {
		mgr.Notify(discovery.DiscoveredPeer{
			ID:       peerID,
			Nickname: peerNickname,
			Addr:     peerUUID,
			Source:   "ble→webrtc",
		})
	}
	s.log("[BLE▸WTC] 연결 완료: " + peerNickname)

	peerKey := transport.PeerID("ble-" + peerUUID)
	conn.OnClose = func() {
		s.registry.Unregister(peerKey)
		_ = s.webrtcT.Close(peerKey)
	}
	s.webrtcT.Attach(peerKey, conn)
	s.registry.Register(peerKey, s.webrtcT)

	// Send Hello frame to the remote peer using the new transport.
	go func() {
		stream, err := s.webrtcT.OpenStream(ctx, peerKey)
		if err != nil {
			return
		}
		defer stream.Close()
		_ = transfer.WriteFrame(stream, proto.Frame{Type: proto.FrameHello, From: s.cfg.Nickname})
	}()
}
