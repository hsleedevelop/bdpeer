package core

import (
	"testing"

	"github.com/hsleedevelop/bdpeer/internal/config"
	"github.com/hsleedevelop/bdpeer/internal/transport"
)

func TestSourceForBLEPeerUsesCurrentTransport(t *testing.T) {
	svc := NewService(&config.Config{Nickname: "alice"}, "")
	peerKey := transport.PeerID("ble-peer")

	if got := svc.sourceForBLEPeer(peerKey); got != "ble" {
		t.Fatalf("sourceForBLEPeer without registry entry = %q, want ble", got)
	}

	svc.registry.Register(peerKey, svc.bleT)
	if got := svc.sourceForBLEPeer(peerKey); got != "ble" {
		t.Fatalf("sourceForBLEPeer with BLE transport = %q, want ble", got)
	}

	svc.registry.Register(peerKey, svc.webrtcT)
	if got := svc.sourceForBLEPeer(peerKey); got != "ble→webrtc" {
		t.Fatalf("sourceForBLEPeer with WebRTC transport = %q, want ble→webrtc", got)
	}
}
