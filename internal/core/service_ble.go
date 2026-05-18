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
// BLETransport is registered immediately on peer found as a fallback; it is
// superseded by WebRTCTransport if the upgrade succeeds.
func (s *Service) startBLEWithWebRTC(ctx context.Context, mgr *discovery.Manager) {
	// Wire BLE data transport handler — same inbound processor as other transports.
	s.bleT.SetHandler(s.onInboundStream)

	// Route incoming BLE data to the transport.
	discovery.SetBLEDataCallback(func(peerUUID string, data []byte) {
		s.bleT.InboundData(transport.PeerID("ble-"+peerUUID), data)
	})

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
			peerKey := transport.PeerID("ble-" + peerUUID)
			// Register BLE data transport immediately as fallback.
			s.bleT.Attach(peerKey, func(data []byte) {
				discovery.BLECentralSendData(peerUUID, data)
			})
			s.registry.Register(peerKey, s.bleT)
			// Make the peer visible in the UI right away (provisional entry —
			// will be refined when WebRTC connects or Hello arrives).
			mgr.Notify(discovery.DiscoveredPeer{
				ID:       peer.ID("ble-" + peerUUID),
				Nickname: nickname,
				Addr:     peerUUID,
				Source:   "ble",
			})
			// Send Hello over BLE so responder gets our nickname.
			go s.sendBLEHello(ctx, peerKey)
			// Start WebRTC upgrade — will overwrite BLE in registry if it succeeds.
			upgrader.OnBLEPeerFound(ctx, nickname, peerUUID)
		},
		func(peerUUID, sdp string, isOffer bool) {
			upgrader.OnSDPReceived(ctx, peerUUID, sdp, isOffer)
		},
		func(centralUUID string) {
			peerKey := transport.PeerID("ble-" + centralUUID)
			s.bleT.Attach(peerKey, func(data []byte) {
				discovery.BLEPeripheralSendDataTo(centralUUID, data)
			})
			s.registry.Register(peerKey, s.bleT)
			go s.sendBLEHello(ctx, peerKey)
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
	_ = s.bleT.Close(peerKey) // BLE superseded by WebRTC for this peer

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

// sendBLEHello sends a Hello frame to a peer over the BLE data transport.
func (s *Service) sendBLEHello(ctx context.Context, peerKey transport.PeerID) {
	stream, err := s.bleT.OpenStream(ctx, peerKey)
	if err != nil {
		return
	}
	defer stream.Close()
	_ = transfer.WriteFrame(stream, proto.Frame{Type: proto.FrameHello, From: s.cfg.Nickname})
}
