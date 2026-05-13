package discovery_test

import (
	"testing"
	"time"

	"github.com/chad/bdpeer/internal/discovery"
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
	m.Notify(discovery.DiscoveredPeer{ID: id, Nickname: "bob", Addrs: []multiaddr.Multiaddr{addr}, Source: "ssdp"}) // duplicate

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
		t.Error("duplicate peer should not fire OnPeerFound again")
	case <-time.After(100 * time.Millisecond):
	}
}
