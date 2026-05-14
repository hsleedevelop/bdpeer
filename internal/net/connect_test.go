package net_test

import (
	"context"
	"testing"
	"time"

	bnet "github.com/hsleedevelop/bdpeer/internal/net"
)

func TestConnectByMultiaddr(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	h1, err := bnet.NewHost(ctx, "alice")
	if err != nil {
		t.Fatal(err)
	}
	defer h1.Close()

	h2, err := bnet.NewHost(ctx, "bob")
	if err != nil {
		t.Fatal(err)
	}
	defer h2.Close()

	addrStr := bnet.FirstTCPAddr(h2)
	if addrStr == "" {
		t.Fatal("h2 has no TCP addresses")
	}

	info, err := h1.ConnectByAddr(ctx, addrStr)
	if err != nil {
		t.Fatalf("ConnectByAddr: %v", err)
	}
	if info.ID != h2.Libp2p.ID() {
		t.Errorf("connected to wrong peer")
	}
}
