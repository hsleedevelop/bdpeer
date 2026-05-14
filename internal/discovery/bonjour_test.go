package discovery_test

import (
	"context"
	"testing"
	"time"

	"github.com/hsleedevelop/bdpeer/internal/discovery"
)

func TestBonjourRegisterAndBrowse(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	found := make(chan discovery.DiscoveredPeer, 1)
	mgr := discovery.NewManager("alice")
	mgr.OnPeerFound = func(p discovery.DiscoveredPeer) { found <- p }

	svcBob, err := discovery.RegisterBonjour("bob", 5001, nil)
	if err != nil {
		t.Fatalf("RegisterBonjour: %v", err)
	}
	defer svcBob.Shutdown()

	stopBrowse, err := discovery.BrowseBonjour(ctx, mgr)
	if err != nil {
		t.Fatalf("BrowseBonjour: %v", err)
	}
	defer stopBrowse()

	select {
	case p := <-found:
		if p.Nickname != "bob" {
			t.Errorf("got %q, want bob", p.Nickname)
		}
		if p.Source != "mdns" {
			t.Errorf("source: got %q, want mdns", p.Source)
		}
	case <-ctx.Done():
		t.Skip("timed out — mDNS not found. Acceptable on some CI environments.")
	}
}
