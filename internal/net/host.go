package net

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"sync"
	"time"

	dht "github.com/libp2p/go-libp2p-kad-dht"
	libp2p "github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	drouting "github.com/libp2p/go-libp2p/p2p/discovery/routing"
	dutil "github.com/libp2p/go-libp2p/p2p/discovery/util"
	"github.com/libp2p/go-libp2p/p2p/host/autorelay"
	"github.com/multiformats/go-multiaddr"

	"github.com/hsleedevelop/bdpeer/internal/discovery"
	"github.com/hsleedevelop/bdpeer/internal/proto"
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
	OnConnected  func(peer.ID)
	OnLog        func(string)
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
		libp2p.EnableHolePunching(),
		libp2p.EnableAutoRelay(autorelay.WithPeerSource(
			func(ctx context.Context, numPeers int) <-chan peer.AddrInfo {
				ch := make(chan peer.AddrInfo)
				go func() {
					defer close(ch)
					for _, maddr := range dht.DefaultBootstrapPeers {
						pi, err := peer.AddrInfoFromP2pAddr(maddr)
						if err != nil {
							continue
						}
						select {
						case ch <- *pi:
						case <-ctx.Done():
							return
						}
					}
				}()
				return ch
			},
		)),
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

func (h *Host) AttachDiscovery(mgr *discovery.Manager) {
	h.Libp2p.Network().Notify(&libp2pNotifee{host: h, mgr: mgr})
}

type libp2pNotifee struct {
	host *Host
	mgr  *discovery.Manager
}

func (n *libp2pNotifee) Connected(_ network.Network, conn network.Conn) {
	id := conn.RemotePeer()
	nick := n.host.NicknameFor(id)
	addrs := n.host.Libp2p.Peerstore().Addrs(id)
	n.host.emitLog("연결됨: " + id.String()[:8] + "... (닉네임 교환 중)")
	n.mgr.Notify(discovery.DiscoveredPeer{ID: id, Nickname: nick, Addrs: addrs, Source: "dht"})
	if n.host.OnConnected != nil {
		go n.host.OnConnected(id)
	}
}
func (n *libp2pNotifee) Disconnected(_ network.Network, conn network.Conn) {
	id := conn.RemotePeer()
	n.host.emitLog("연결 끊김: " + id.String()[:8] + "...")
	n.mgr.Forget(id.String())
}
func (n *libp2pNotifee) Listen(_ network.Network, _ multiaddr.Multiaddr)      {}
func (n *libp2pNotifee) ListenClose(_ network.Network, _ multiaddr.Multiaddr) {}

const dhtNamespace = "bdpeer/v1"

func (h *Host) emitLog(msg string) {
	if h.OnLog != nil {
		h.OnLog(msg)
	}
}

// StartDHTDiscovery advertises on the DHT and periodically finds peers.
func (h *Host) StartDHTDiscovery(ctx context.Context, mgr *discovery.Manager) {
	rd := drouting.NewRoutingDiscovery(h.dht)
	h.emitLog("DHT 부트스트랩 대기 중 (약 10초)...")

	go func() {
		select {
		case <-time.After(10 * time.Second):
		case <-ctx.Done():
			return
		}
		// Advertise AFTER bootstrap so the DHT is ready to store the provide record
		dutil.Advertise(ctx, rd, dhtNamespace)
		addrs := h.Libp2p.Addrs()
		addrStrs := make([]string, 0, len(addrs))
		for _, a := range addrs {
			addrStrs = append(addrStrs, a.String())
		}
		h.emitLog("DHT 광고 시작 (bdpeer/v1), 내 주소: " + fmt.Sprintf("%v", addrStrs))
		h.emitLog("DHT 준비 완료, 피어 검색 시작")
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			h.findAndConnectDHTPeers(ctx, rd, mgr)
			select {
			case <-ticker.C:
			case <-ctx.Done():
				return
			}
		}
	}()
}

func (h *Host) findAndConnectDHTPeers(ctx context.Context, rd *drouting.RoutingDiscovery, mgr *discovery.Manager) {
	h.emitLog("DHT 피어 검색 중...")
	findCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	peers, err := dutil.FindPeers(findCtx, rd, dhtNamespace)
	if err != nil {
		h.emitLog("DHT 검색 실패: " + err.Error())
		return
	}

	newPeers := 0
	for _, p := range peers {
		if p.ID == h.Libp2p.ID() || len(p.Addrs) == 0 {
			continue
		}
		if h.Libp2p.Network().Connectedness(p.ID) == network.Connected {
			continue
		}
		newPeers++
		h.emitLog("DHT 발견 → " + p.ID.String()[:8] + "... 연결 시도 중")
		connCtx, connCancel := context.WithTimeout(ctx, 15*time.Second)
		if err := h.Libp2p.Connect(connCtx, p); err != nil {
			h.emitLog("DHT 연결 실패: " + p.ID.String()[:8] + "... — " + err.Error())
		}
		connCancel()
	}
	if newPeers == 0 {
		h.emitLog("DHT: 새 피어 없음 (30초 후 재시도)")
	}
}

// FirstTCPAddr returns the best TCP listen multiaddr of h (with peer ID appended).
// Prefers non-loopback addresses so the displayed address is reachable from other machines.
func FirstTCPAddr(h *Host) string {
	pid := h.Libp2p.ID()
	var loopback string
	for _, a := range h.Libp2p.Addrs() {
		if _, err := a.ValueForProtocol(multiaddr.P_TCP); err != nil {
			continue
		}
		ip4, err := a.ValueForProtocol(multiaddr.P_IP4)
		if err != nil {
			continue
		}
		full := a.String() + "/p2p/" + pid.String()
		if ip4 == "127.0.0.1" {
			if loopback == "" {
				loopback = full
			}
			continue
		}
		return full
	}
	return loopback
}
