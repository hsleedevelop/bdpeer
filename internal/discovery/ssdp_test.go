package discovery_test

import (
	"context"
	"testing"
	"time"

	"github.com/hsleedevelop/bdpeer/internal/discovery"
)

func TestSSDPAdvertiseAndSearch(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	found := make(chan discovery.DiscoveredPeer, 1)
	mgr := discovery.NewManager("alice")
	mgr.OnPeerFound = func(p discovery.DiscoveredPeer) { found <- p }

	stopAdv, err := discovery.AdvertiseSSDP(ctx, "bob", "127.0.0.1", 5002, "")
	if err != nil {
		t.Fatalf("AdvertiseSSDP: %v", err)
	}
	defer stopAdv()

	time.Sleep(200 * time.Millisecond)

	stopSearch, err := discovery.SearchSSDP(ctx, mgr)
	if err != nil {
		t.Fatalf("SearchSSDP: %v", err)
	}
	defer stopSearch()

	select {
	case p := <-found:
		if p.Source != "ssdp" {
			t.Errorf("source: got %q, want ssdp", p.Source)
		}
	case <-ctx.Done():
		t.Skip("SSDP not received — may require multicast on this network interface")
	}
}
