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
	// Merge with an existing entry under a different key but same nickname —
	// e.g. BLE provisional entry (peripheral UUID) being upgraded by an
	// inbound Hello arriving under the remote's central UUID. Keeping the
	// original key prevents duplicate UI rows.
	if p.Nickname != "" {
		for k, existing := range m.seen {
			if k == key || existing.Nickname != p.Nickname {
				continue
			}
			merged := existing
			if p.Source != "" {
				merged.Source = p.Source
			}
			if len(p.Addrs) > 0 {
				merged.Addrs = p.Addrs
			}
			if p.Addr != "" {
				merged.Addr = p.Addr
			}
			changed := merged.Source != existing.Source ||
				len(merged.Addrs) != len(existing.Addrs) ||
				merged.Addr != existing.Addr
			m.seen[k] = merged
			m.mu.Unlock()
			if changed && m.OnPeerFound != nil {
				m.OnPeerFound(merged)
			}
			return
		}
	}
	existing, exists := m.seen[key]
	if !exists {
		m.seen[key] = p
		m.mu.Unlock()
		if m.OnPeerFound != nil {
			m.OnPeerFound(p)
		}
		return
	}
	merged := existing
	if p.Source != "" {
		merged.Source = p.Source
	}
	if p.Nickname != "" {
		merged.Nickname = p.Nickname
	}
	if len(p.Addrs) > 0 {
		merged.Addrs = p.Addrs
	}
	changed := merged.Source != existing.Source ||
		merged.Nickname != existing.Nickname ||
		len(merged.Addrs) != len(existing.Addrs)
	m.seen[key] = merged
	m.mu.Unlock()
	if changed && m.OnPeerFound != nil {
		m.OnPeerFound(merged)
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
