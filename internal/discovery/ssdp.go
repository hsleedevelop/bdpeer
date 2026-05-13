package discovery

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/koron/go-ssdp"
	"github.com/multiformats/go-multiaddr"
)

const ssdpType = "urn:bdpeer-org:device:BdPeer:1"

func AdvertiseSSDP(ctx context.Context, nickname string, port int) (stop func(), err error) {
	ad, err := ssdp.Advertise(
		ssdpType,
		fmt.Sprintf("uuid:bdpeer-%s", nickname),
		fmt.Sprintf("http://0.0.0.0:%d/bdpeer.xml", port),
		fmt.Sprintf("bdpeer/%s", nickname),
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
			nickname := parseSSDPNickname(srv.Server)
			ma := parseSSDPAddr(srv.Location)
			if nickname == "" || ma == nil {
				continue
			}
			mgr.Notify(DiscoveredPeer{Nickname: nickname, Addrs: []multiaddr.Multiaddr{ma}, Source: "ssdp"})
		}

		mon := &ssdp.Monitor{
			Alive: func(m *ssdp.AliveMessage) {
				if m.Type != ssdpType {
					return
				}
				nick := parseSSDPNickname(m.Server)
				ma := parseSSDPAddr(m.Location)
				if nick != "" && ma != nil {
					mgr.Notify(DiscoveredPeer{Nickname: nick, Addrs: []multiaddr.Multiaddr{ma}, Source: "ssdp"})
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

func parseSSDPNickname(server string) string {
	if idx := strings.Index(server, "bdpeer/"); idx >= 0 {
		return server[idx+7:]
	}
	return ""
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
