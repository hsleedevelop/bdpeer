package net

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"sync"

	dht "github.com/libp2p/go-libp2p-kad-dht"
	libp2p "github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"

	"github.com/chad/bdpeer/internal/proto"
)

type PeerInfo struct {
	ID       peer.ID
	Nickname string
	Addrs    []multiaddr.Multiaddr
	Source   string
}

type Host struct {
	Libp2p       host.Host
	Nickname     string
	OnFrame      func(peer.ID, proto.Frame)
	OnFileStream func(peer.ID, proto.Frame, io.Reader)
	mu           sync.RWMutex
	nicknames    map[peer.ID]string
	dht          *dht.IpfsDHT
}

func GenerateIdentityB64() (string, error) {
	priv, _, err := crypto.GenerateKeyPair(crypto.Ed25519, -1)
	if err != nil {
		return "", err
	}
	raw, err := crypto.MarshalPrivateKey(priv)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(raw), nil
}

func PrivateKeyFromB64(encoded string) (crypto.PrivKey, error) {
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, err
	}
	return crypto.UnmarshalPrivateKey(raw)
}

func NewHost(ctx context.Context, nickname string) (*Host, error) {
	priv, _, err := crypto.GenerateKeyPair(crypto.Ed25519, -1)
	if err != nil {
		return nil, fmt.Errorf("generate key: %w", err)
	}
	return NewHostWithIdentity(ctx, nickname, priv)
}

func NewHostWithIdentity(ctx context.Context, nickname string, priv crypto.PrivKey) (*Host, error) {
	h, err := libp2p.New(
		libp2p.Identity(priv),
		libp2p.ListenAddrStrings(
			"/ip4/0.0.0.0/tcp/0",
			"/ip4/0.0.0.0/udp/0/quic-v1",
		),
		libp2p.NATPortMap(),
	)
	if err != nil {
		return nil, fmt.Errorf("new libp2p host: %w", err)
	}

	kd, err := dht.New(ctx, h, dht.Mode(dht.ModeAuto))
	if err != nil {
		h.Close()
		return nil, fmt.Errorf("new DHT: %w", err)
	}

	if err := kd.Bootstrap(ctx); err != nil {
		h.Close()
		return nil, fmt.Errorf("DHT bootstrap: %w", err)
	}

	return &Host{Libp2p: h, Nickname: nickname, nicknames: make(map[peer.ID]string), dht: kd}, nil
}

func (h *Host) RememberNickname(id peer.ID, nickname string) {
	if id == "" || nickname == "" {
		return
	}
	h.mu.Lock()
	h.nicknames[id] = nickname
	h.mu.Unlock()
}

func (h *Host) NicknameFor(id peer.ID) string {
	h.mu.RLock()
	nick := h.nicknames[id]
	h.mu.RUnlock()
	if nick != "" {
		return nick
	}
	if id != "" {
		s := id.String()
		if len(s) > 8 {
			return s[:8]
		}
		return s
	}
	return "unknown"
}

func (h *Host) Close() error {
	if h.dht != nil {
		h.dht.Close()
	}
	return h.Libp2p.Close()
}

// ConnectByAddr connects to a peer by full multiaddr string (e.g., /ip4/1.2.3.4/tcp/4001/p2p/QmXxx)
func (h *Host) ConnectByAddr(ctx context.Context, addrStr string) (PeerInfo, error) {
	ma, err := multiaddr.NewMultiaddr(addrStr)
	if err != nil {
		return PeerInfo{}, fmt.Errorf("parse addr %q: %w", addrStr, err)
	}
	pi, err := peer.AddrInfoFromP2pAddr(ma)
	if err != nil {
		return PeerInfo{}, fmt.Errorf("addr info from %q: %w", addrStr, err)
	}
	if err := h.Libp2p.Connect(ctx, *pi); err != nil {
		return PeerInfo{}, fmt.Errorf("connect: %w", err)
	}
	nickname := h.NicknameFor(pi.ID)
	return PeerInfo{ID: pi.ID, Nickname: nickname, Addrs: pi.Addrs}, nil
}

// FirstTCPAddr returns the first TCP listen multiaddr of h (with peer ID appended).
func FirstTCPAddr(h *Host) string {
	pid := h.Libp2p.ID()
	for _, a := range h.Libp2p.Addrs() {
		if _, err := a.ValueForProtocol(multiaddr.P_TCP); err == nil {
			return a.String() + "/p2p/" + pid.String()
		}
	}
	return ""
}
