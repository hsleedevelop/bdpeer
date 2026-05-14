package net

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/hsleedevelop/bdpeer/internal/config"
)

// BLEWebRTCUpgrader manages WebRTC connection upgrades triggered by BLE discovery.
// Flow:
//
//	Initiator (lower nickname alphabetically):
//	  BLE peer found → create offer → send via BLE → receive answer → connected
//
//	Responder (higher nickname):
//	  BLE central subscribed → wait for offer → create answer → send via BLE → connected
type BLEWebRTCUpgrader struct {
	myNickname  string
	mu          sync.Mutex
	TURNServers []config.TURNServer
	// peerUUID → pending offer conn (initiator side, waiting for answer)
	pendingOffers map[string]*WebRTCConn
	// OnConnected is called when WebRTC data channel is ready.
	// source is "ble→webrtc".
	OnConnected func(peerUUID, peerNickname string, conn *WebRTCConn)
	OnLog       func(string)

	// BLE send functions (wired by service layer)
	SendOfferFn  func(peerUUID, sdp string)
	SendAnswerFn func(sdp string) // broadcast to all centrals
}

const (
	gatherTimeout  = 15 * time.Second // ICE candidate gathering (STUN + TURN)
	connectTimeout = 90 * time.Second // full handshake + ICE connection via relay
)

func NewBLEWebRTCUpgrader(myNickname string) *BLEWebRTCUpgrader {
	return &BLEWebRTCUpgrader{
		myNickname:    myNickname,
		pendingOffers: make(map[string]*WebRTCConn),
	}
}

func (u *BLEWebRTCUpgrader) log(msg string) {
	if u.OnLog != nil {
		u.OnLog(msg)
	}
}

// isInitiator decides who creates the offer: lower nickname alphabetically.
func (u *BLEWebRTCUpgrader) isInitiator(peerNickname string) bool {
	return u.myNickname < peerNickname
}

// OnBLEPeerFound is called when BLE discovers a peer.
// If we are the initiator, create and send an offer.
func (u *BLEWebRTCUpgrader) OnBLEPeerFound(ctx context.Context, peerNickname, peerUUID string) {
	if !u.isInitiator(peerNickname) {
		u.log(fmt.Sprintf("[BLE] %s 발견 → 응답자 대기 중", peerNickname))
		return
	}

	u.mu.Lock()
	if _, exists := u.pendingOffers[peerUUID]; exists {
		u.mu.Unlock()
		return // already in progress
	}
	u.mu.Unlock()

	u.log(fmt.Sprintf("[BLE] %s 발견 → WebRTC offer 생성 중", peerNickname))

	// connectCtx covers the entire handshake + ICE connection lifecycle.
	connectCtx, connectCancel := context.WithTimeout(ctx, connectTimeout)
	go func() {
		defer connectCancel()

		// Gathering gets a shorter deadline: just candidate collection.
		gatherCtx, gatherCancel := context.WithTimeout(connectCtx, gatherTimeout)
		conn, offerSDP, err := NewWebRTCOffer(gatherCtx, u.TURNServers)
		gatherCancel()
		if err != nil {
			u.log("[BLE→WTC] offer 실패: " + err.Error())
			return
		}

		u.mu.Lock()
		u.pendingOffers[peerUUID] = conn
		u.mu.Unlock()

		if u.SendOfferFn != nil {
			u.SendOfferFn(peerUUID, offerSDP)
			u.log("[BLE→WTC] offer 전송 완료 → answer 대기")
		}

		// Wait for ICE connection — answer arrives via OnSDPReceived → SetAnswer.
		select {
		case <-conn.Connected():
			u.log("[BLE→WTC] " + peerNickname + " 연결 성공!")
			if u.OnConnected != nil {
				u.OnConnected(peerUUID, peerNickname, conn)
			}
		case <-connectCtx.Done():
			u.log("[BLE→WTC] 연결 타임아웃: " + peerNickname)
			conn.Close()
			u.mu.Lock()
			delete(u.pendingOffers, peerUUID)
			u.mu.Unlock()
		}
	}()
}

// OnSDPReceived handles incoming SDP (offer or answer) from BLE.
func (u *BLEWebRTCUpgrader) OnSDPReceived(ctx context.Context, peerUUID, sdp string, isOffer bool) {
	if isOffer {
		// We are the responder: create answer.
		u.log("[BLE→WTC] offer 수신 → answer 생성 중")
		go func() {
			// Gathering uses short timeout; connection uses full connectTimeout.
			connectCtx, connectCancel := context.WithTimeout(ctx, connectTimeout)
			defer connectCancel()

			gatherCtx, gatherCancel := context.WithTimeout(connectCtx, gatherTimeout)
			conn, answerSDP, err := NewWebRTCAnswer(gatherCtx, sdp, u.TURNServers)
			gatherCancel()
			if err != nil {
				u.log("[BLE→WTC] answer 실패: " + err.Error())
				return
			}

			if u.SendAnswerFn != nil {
				u.SendAnswerFn(answerSDP)
				u.log("[BLE→WTC] answer 전송 완료")
			}

			select {
			case <-conn.Connected():
				u.log("[BLE→WTC] 연결 성공 (응답자)")
				if u.OnConnected != nil {
					u.OnConnected(peerUUID, "", conn)
				}
			case <-connectCtx.Done():
				u.log("[BLE→WTC] 연결 타임아웃 (응답자)")
				conn.Close()
			}
		}()
		return
	}

	// We are the initiator: set the answer.
	u.mu.Lock()
	conn, ok := u.pendingOffers[peerUUID]
	if ok {
		delete(u.pendingOffers, peerUUID)
	}
	u.mu.Unlock()

	if !ok {
		u.log("[BLE→WTC] answer 수신했지만 대응하는 offer 없음")
		return
	}

	u.log("[BLE→WTC] answer 수신 → 핸드셰이크 완료")
	if err := conn.SetAnswer(sdp); err != nil {
		u.log("[BLE→WTC] SetAnswer 실패: " + err.Error())
		conn.Close()
	}
}
