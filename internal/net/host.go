package net

import (
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"
)

type PeerInfo struct {
	ID       peer.ID
	Nickname string
	Addrs    []multiaddr.Multiaddr
	Source   string
}
