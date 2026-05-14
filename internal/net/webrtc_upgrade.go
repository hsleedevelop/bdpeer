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

	ctx2, cancel := context.WithTimeout(ctx, 30*time.Second)
	go func() {
		defer cancel()
		conn, offerSDP, err := NewWebRTCOffer(ctx2, u.TURNServers)
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

		// Wait for connection (answer will arrive via OnSDPReceived)
		select {
		case <-conn.Connected():
			u.log("[BLE→WTC] " + peerNickname + " 연결 성공!")
			if u.OnConnected != nil {
				u.OnConnected(peerUUID, peerNickname, conn)
			}
		case <-ctx2.Done():
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
			ctx2, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()

			conn, answerSDP, err := NewWebRTCAnswer(ctx2, sdp, u.TURNServers)
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
			case <-ctx2.Done():
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
