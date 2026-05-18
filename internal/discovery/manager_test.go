package discovery_test

import (
	"testing"
	"time"

	"github.com/hsleedevelop/bdpeer/internal/discovery"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"
)

func TestManagerNotify(t *testing.T) {
	m := discovery.NewManager("alice")
	found := make(chan discovery.DiscoveredPeer, 4)
	m.OnPeerFound = func(p discovery.DiscoveredPeer) { found <- p }

	id := peer.ID("bob-peer")
	addr, _ := multiaddr.NewMultiaddr("/ip4/192.168.1.2/tcp/4001")

	m.Notify(discovery.DiscoveredPeer{ID: id, Nickname: "bob", Addrs: []multiaddr.Multiaddr{addr}, Source: "mdns"})
	m.Notify(discovery.DiscoveredPeer{ID: id, Nickname: "bob", Addrs: []multiaddr.Multiaddr{addr}, Source: "mdns"}) // identical rebroadcast

	select {
	case p := <-found:
		if p.Nickname != "bob" {
			t.Errorf("got %q, want bob", p.Nickname)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}

	select {
	case <-found:
		t.Error("identical rebroadcast should not fire OnPeerFound again")
	case <-time.After(100 * time.Millisecond):
	}
}

// Source change (e.g. BLE provisional → BLE▸WebRTC upgrade) must refire so
// the UI can update the peer's badge in place.
func TestManagerNotifySourceUpgrade(t *testing.T) {
	m := discovery.NewManager("alice")
	found := make(chan discovery.DiscoveredPeer, 4)
	m.OnPeerFound = func(p discovery.DiscoveredPeer) { found <- p }

	id := peer.ID("ble-abc")
	m.Notify(discovery.DiscoveredPeer{ID: id, Nickname: "bob", Source: "ble", Addr: "abc"})
	m.Notify(discovery.DiscoveredPeer{ID: id, Nickname: "bob", Source: "ble→webrtc", Addr: "abc"})

	<-found
	select {
	case p := <-found:
		if p.Source != "ble→webrtc" {
			t.Errorf("source=%q, want ble→webrtc", p.Source)
		}
	case <-time.After(time.Second):
		t.Fatal("source upgrade should fire OnPeerFound")
	}
}

// Inbound Hello can arrive under a different transport key (remote central UUID)
// than the provisional discovery key (remote peripheral UUID). Manager must
// merge by nickname instead of creating a duplicate entry.
func TestManagerNotifyNicknameMerge(t *testing.T) {
	m := discovery.NewManager("alice")
	found := make(chan discovery.DiscoveredPeer, 4)
	m.OnPeerFound = func(p discovery.DiscoveredPeer) { found <- p }

	m.Notify(discovery.DiscoveredPeer{ID: peer.ID("ble-peripheral-uuid"), Nickname: "bob", Source: "ble"})
	m.Notify(discovery.DiscoveredPeer{ID: peer.ID("ble-central-uuid"), Nickname: "bob", Source: "ble→webrtc"})

	first := <-found
	if first.ID != peer.ID("ble-peripheral-uuid") {
		t.Fatalf("first ID=%q, want ble-peripheral-uuid", first.ID)
	}
	select {
	case second := <-found:
		if second.ID != peer.ID("ble-peripheral-uuid") {
			t.Errorf("merged entry must keep original ID, got %q", second.ID)
		}
		if second.Source != "ble→webrtc" {
			t.Errorf("merged source=%q, want ble→webrtc", second.Source)
		}
	case <-time.After(time.Second):
		t.Fatal("nickname-matched notify should fire OnPeerFound for badge update")
	}
}
