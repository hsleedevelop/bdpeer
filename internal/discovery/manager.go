package discovery

import (
	"sync"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"
)

type DiscoveredPeer struct {
	ID       peer.ID
	Nickname string
	Addrs    []multiaddr.Multiaddr
	Addr     string // fallback for non-libp2p proximity signals (BLE)
	Source   string // "mdns", "ssdp", "wsd", "ble", "dht"
}

type Manager struct {
	nickname    string
	mu          sync.Mutex
	seen        map[string]DiscoveredPeer
	OnPeerFound func(DiscoveredPeer)
	OnPeerLost  func(DiscoveredPeer)
}

func NewManager(nickname string) *Manager {
	return &Manager{
		nickname: nickname,
		seen:     make(map[string]DiscoveredPeer),
	}
}

func (m *Manager) Notify(p DiscoveredPeer) {
	if p.Nickname == m.nickname {
		return
	}
	key := peerKey(p)
	if key == "" {
		return
	}
	m.mu.Lock()
	_, exists := m.seen[key]
	if !exists {
		m.seen[key] = p
	}
	m.mu.Unlock()

	if !exists && m.OnPeerFound != nil {
		m.OnPeerFound(p)
	}
}

func (m *Manager) Forget(key string) {
	m.mu.Lock()
	p, exists := m.seen[key]
	if exists {
		delete(m.seen, key)
	}
	m.mu.Unlock()

	if exists && m.OnPeerLost != nil {
		m.OnPeerLost(p)
	}
}

func peerKey(p DiscoveredPeer) string {
	if p.ID != "" {
		return p.ID.String()
	}
	if len(p.Addrs) > 0 {
		return p.Addrs[0].String()
	}
	if p.Addr != "" {
		return p.Source + ":" + p.Addr
	}
	return ""
}
