package net_test

import (
	"context"
	"testing"
	"time"

	bnet "github.com/chad/bdpeer/internal/net"
)

func TestNewHost(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	h, err := bnet.NewHost(ctx, "alice")
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	defer h.Close()

	if h.Libp2p.ID() == "" {
		t.Error("host ID should not be empty")
	}
	if len(h.Libp2p.Addrs()) == 0 {
		t.Error("host should have at least one listen addr")
	}
	if h.Nickname != "alice" {
		t.Errorf("got nickname %q, want %q", h.Nickname, "alice")
	}
}
