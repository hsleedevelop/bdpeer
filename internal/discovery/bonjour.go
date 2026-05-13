package discovery

import (
	"context"
	"fmt"
	"net"
	"strings"

	"github.com/grandcat/zeroconf"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"
)

const bonjourService = "_bdpeer._tcp"
const bonjourDomain = "local."

func RegisterBonjour(nickname string, port int, fullAddrs []multiaddr.Multiaddr) (*zeroconf.Server, error) {
	txt := []string{"txtv=0", "app=bdpeer"}
	for _, addr := range fullAddrs {
		txt = append(txt, "addr="+addr.String())
	}
	return zeroconf.Register(nickname, bonjourService, bonjourDomain, port, txt, nil)
}

func BrowseBonjour(ctx context.Context, mgr *Manager) (stop func(), err error) {
	resolver, err := zeroconf.NewResolver(nil)
	if err != nil {
		return nil, fmt.Errorf("zeroconf resolver: %w", err)
	}

	entries := make(chan *zeroconf.ServiceEntry)
	go func() {
		for entry := range entries {
			if len(entry.AddrIPv4) == 0 && len(entry.AddrIPv6) == 0 {
				continue
			}
			var ip net.IP
			if len(entry.AddrIPv4) > 0 {
				ip = entry.AddrIPv4[0]
			} else {
				ip = entry.AddrIPv6[0]
			}
			ma := bonjourMultiaddr(entry)
			if ma == nil {
				var parseErr error
				ma, parseErr = multiaddr.NewMultiaddr("/ip4/" + ip.String() + "/tcp/" + fmt.Sprint(entry.Port))
				if parseErr != nil {
					continue
				}
			}
			var id peer.ID
			if info, err := peer.AddrInfoFromP2pAddr(ma); err == nil {
				id = info.ID
			}
			mgr.Notify(DiscoveredPeer{
				ID:       id,
				Nickname: entry.Instance,
				Addrs:    []multiaddr.Multiaddr{ma},
				Source:   "mdns",
			})
		}
	}()

	browseCtx, cancel := context.WithCancel(ctx)
	go func() {
		_ = resolver.Browse(browseCtx, bonjourService, bonjourDomain, entries)
	}()

	return cancel, nil
}

func bonjourMultiaddr(entry *zeroconf.ServiceEntry) multiaddr.Multiaddr {
	for _, txt := range entry.Text {
		if strings.HasPrefix(txt, "addr=") {
			ma, err := multiaddr.NewMultiaddr(strings.TrimPrefix(txt, "addr="))
			if err == nil {
				return ma
			}
		}
	}
	return nil
}
