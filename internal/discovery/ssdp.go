package discovery

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/koron/go-ssdp"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"
)

const ssdpType = "urn:bdpeer-org:device:BdPeer:1"

// AdvertiseSSDP announces this peer on the local network. ip should be a
// routable interface address (not 0.0.0.0); peerID is the libp2p host ID so
// receivers can construct a full multiaddr and dial back.
func AdvertiseSSDP(ctx context.Context, nickname, ip string, port int, peerID string) (stop func(), err error) {
	if ip == "" {
		ip = "0.0.0.0"
	}
	server := fmt.Sprintf("bdpeer/%s", nickname)
	if peerID != "" {
		server = fmt.Sprintf("bdpeer/%s/%s", nickname, peerID)
	}
	ad, err := ssdp.Advertise(
		ssdpType,
		fmt.Sprintf("uuid:bdpeer-%s", nickname),
		fmt.Sprintf("http://%s:%d/bdpeer.xml", ip, port),
		server,
		1800,
	)
	if err != nil {
		return nil, fmt.Errorf("ssdp advertise: %w", err)
	}

	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				ad.Close()
				return
			case <-ticker.C:
				_ = ad.Alive()
			}
		}
	}()

	return func() { ad.Close() }, nil
}

func SearchSSDP(ctx context.Context, mgr *Manager) (stop func(), err error) {
	stopCtx, cancel := context.WithCancel(ctx)

	go func() {
		list, err := ssdp.Search(ssdpType, 3, "")
		if err != nil {
			return
		}
		for _, srv := range list {
			nickname, pid := parseSSDPServer(srv.Server)
			ma := parseSSDPAddr(srv.Location)
			if nickname == "" || ma == nil {
				continue
			}
			mgr.Notify(DiscoveredPeer{ID: pid, Nickname: nickname, Addrs: []multiaddr.Multiaddr{ma}, Source: "ssdp"})
		}

		mon := &ssdp.Monitor{
			Alive: func(m *ssdp.AliveMessage) {
				if m.Type != ssdpType {
					return
				}
				nick, pid := parseSSDPServer(m.Server)
				ma := parseSSDPAddr(m.Location)
				if nick != "" && ma != nil {
					mgr.Notify(DiscoveredPeer{ID: pid, Nickname: nick, Addrs: []multiaddr.Multiaddr{ma}, Source: "ssdp"})
				}
			},
		}
		if err := mon.Start(); err != nil {
			return
		}
		defer mon.Close()
		<-stopCtx.Done()
	}()

	return cancel, nil
}

// parseSSDPServer parses "bdpeer/<nickname>" or "bdpeer/<nickname>/<peerID>".
// Returns ("", "") when the prefix is missing.
func parseSSDPServer(server string) (nickname string, pid peer.ID) {
	idx := strings.Index(server, "bdpeer/")
	if idx < 0 {
		return "", ""
	}
	rest := server[idx+len("bdpeer/"):]
	if slash := strings.Index(rest, "/"); slash >= 0 {
		nickname = rest[:slash]
		if decoded, err := peer.Decode(rest[slash+1:]); err == nil {
			pid = decoded
		}
	} else {
		nickname = rest
	}
	return nickname, pid
}

func parseSSDPAddr(location string) multiaddr.Multiaddr {
	u, err := url.Parse(location)
	if err != nil {
		return nil
	}
	host := u.Hostname()
	port := u.Port()
	if host == "" || port == "" {
		return nil
	}
	ma, err := multiaddr.NewMultiaddr("/ip4/" + host + "/tcp/" + port)
	if err != nil {
		return nil
	}
	return ma
}
