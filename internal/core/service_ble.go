//go:build ble && darwin

package core

import (
	"bytes"
	"context"
	"io"

	"github.com/hsleedevelop/bdpeer/internal/discovery"
	bnet "github.com/hsleedevelop/bdpeer/internal/net"
	"github.com/hsleedevelop/bdpeer/internal/proto"
	"github.com/hsleedevelop/bdpeer/internal/transfer"
)

// startBLEWithWebRTC wires CoreBluetooth BLE discovery to the BLEWebRTCUpgrader,
// then starts BLE peripheral+central. When a WebRTC data channel is ready the
// peer is registered with the Manager and the bdpeer Hello frame is exchanged.
func (s *Service) startBLEWithWebRTC(ctx context.Context, mgr *discovery.Manager) {
	upgrader := bnet.NewBLEWebRTCUpgrader(s.cfg.Nickname)
	upgrader.OnLog = s.log
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

// handleWebRTCConn registers a newly connected WebRTC peer with the Manager,
// exchanges Hello frames, and handles incoming frames (text/file).
func (s *Service) handleWebRTCConn(ctx context.Context, peerUUID, peerNickname string, conn *bnet.WebRTCConn, mgr *discovery.Manager) {
	mgr.Notify(discovery.DiscoveredPeer{
		Nickname: peerNickname,
		Source:   "ble→webrtc",
	})
	s.log("[BLE▸WTC] 연결 완료: " + peerNickname)

	// Register conn so Send() can reach this peer.
	s.mu.Lock()
	s.webrtcConns[peerUUID] = conn
	s.mu.Unlock()

	// Send Hello frame to the remote peer.
	var helloBuf bytes.Buffer
	_ = transfer.WriteFrame(&helloBuf, proto.Frame{Type: proto.FrameHello, From: s.cfg.Nickname})
	conn.Write(helloBuf.Bytes()) //nolint:errcheck

	// Read loop.
	go func() {
		defer func() {
			conn.Close()
			s.mu.Lock()
			delete(s.webrtcConns, peerUUID)
			s.mu.Unlock()
		}()

		for {
			frame, err := transfer.ReadFrame(conn)
			if err != nil {
				if err != io.EOF {
					s.log("[BLE▸WTC] 수신 오류: " + err.Error())
				}
				return
			}
			switch frame.Type {
			case proto.FrameText:
				s.events <- Event{Type: EventTextReceived, From: frame.From, Content: frame.Content}

			case proto.FrameHello:
				if frame.From != "" && peerNickname == "" {
					mgr.Notify(discovery.DiscoveredPeer{
						Nickname: frame.From,
						Source:   "ble→webrtc",
					})
				}

			case proto.FrameFileStart:
				s.events <- Event{Type: EventFileStart, From: frame.From, Name: frame.Name, Size: frame.Size}
				// Reconstruct the stream: re-encode startFrame then append remaining conn data.
				var startBuf bytes.Buffer
				_ = transfer.WriteFrame(&startBuf, frame)
				combined := io.MultiReader(&startBuf, conn)
				savePath, err := transfer.ReadFile(combined, s.recvDir, func(recv, total int64) {
					s.events <- Event{Type: EventFileProgress, From: frame.From, Received: recv, Total: total}
				})
				if err != nil {
					s.events <- Event{Type: EventError, Err: err}
					return
				}
				s.events <- Event{Type: EventFileDone, From: frame.From, Name: frame.Name, Path: savePath}
			}
			_ = ctx
		}
	}()
}
