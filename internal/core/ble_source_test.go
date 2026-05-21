package core

import (
	"testing"

	"github.com/hsleedevelop/bdpeer/internal/config"
	"github.com/hsleedevelop/bdpeer/internal/transport"
	"github.com/libp2p/go-libp2p/core/peer"
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

func TestTransportKeysForPeerPrefersRawBLEID(t *testing.T) {
	raw := transport.PeerID("ble-5teM3WApK2LXYPnWVY5ExcJMbuc7upU9ort5CjzmHB1X4Xjq")
	keys := transportKeysForPeer(peer.ID(raw))

	if len(keys) == 0 {
		t.Fatal("transportKeysForPeer returned no keys")
	}
	if keys[0] != raw {
		t.Fatalf("first key = %q, want raw BLE key %q", keys[0], raw)
	}
}
