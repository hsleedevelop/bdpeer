package core

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/chad/bdpeer/internal/config"
	"github.com/chad/bdpeer/internal/discovery"
	bnet "github.com/chad/bdpeer/internal/net"
	"github.com/chad/bdpeer/internal/proto"
	"github.com/chad/bdpeer/internal/transfer"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"
	maddr "github.com/multiformats/go-multiaddr"
)

type EventType string

const (
	EventPeerFound    EventType = "peer_found"
	EventPeerLost     EventType = "peer_lost"
	EventTextReceived EventType = "text_received"
	EventFileStart    EventType = "file_start"
	EventFileProgress EventType = "file_progress"
	EventFileDone     EventType = "file_done"
	EventError        EventType = "error"
)

type Event struct {
	Type     EventType
	Peer     bnet.PeerInfo
	From     string
	Content  string
	Name     string
	Size     int64
	Received int64
	Total    int64
	Path     string
	Err      error
}

type SendRequest struct {
	To      peer.ID
	Content string
	File    string
}

type Service struct {
	cfg     *config.Config
	cfgPath string
	host    *bnet.Host
	recvDir string
	stops   []func()
	events  chan Event
}

func NewService(cfg *config.Config, cfgPath string) *Service {
	return &Service{cfg: cfg, cfgPath: cfgPath, events: make(chan Event, 256)}
}

func (s *Service) Events() <-chan Event { return s.events }

func (s *Service) Start(ctx context.Context) error {
	if s.cfg.PrivateKeyB64 == "" {
		encoded, err := bnet.GenerateIdentityB64()
		if err != nil {
			return fmt.Errorf("generate identity: %w", err)
		}
		s.cfg.PrivateKeyB64 = encoded
		if s.cfgPath != "" {
			_ = s.cfg.Save(s.cfgPath)
		}
	}
	priv, err := bnet.PrivateKeyFromB64(s.cfg.PrivateKeyB64)
	if err != nil {
		return fmt.Errorf("load identity: %w", err)
	}

	s.host, err = bnet.NewHostWithIdentity(ctx, s.cfg.Nickname, priv)
	if err != nil {
		return fmt.Errorf("new host: %w", err)
	}

	if s.recvDir == "" {
		if s.cfg.DataDir != "" {
			s.recvDir = filepath.Join(s.cfg.DataDir, "received")
		} else {
			home, _ := os.UserHomeDir()
			s.recvDir = filepath.Join(home, "Downloads", "bdpeer")
		}
	}
	_ = os.MkdirAll(s.recvDir, 0o755)

	s.host.OnFrame = func(_ peer.ID, frame proto.Frame) {
		switch frame.Type {
		case proto.FrameText:
			s.events <- Event{Type: EventTextReceived, From: frame.From, Content: frame.Content}
		}
	}
	s.host.OnFileStream = func(_ peer.ID, startFrame proto.Frame, r io.Reader) {
		s.events <- Event{Type: EventFileStart, From: startFrame.From, Name: startFrame.Name, Size: startFrame.Size}

		var startBuf bytes.Buffer
		_ = transfer.WriteFrame(&startBuf, startFrame)
		combined := io.MultiReader(&startBuf, r)

		savePath, err := transfer.ReadFile(combined, s.recvDir, func(recv, total int64) {
			s.events <- Event{Type: EventFileProgress, From: startFrame.From, Received: recv, Total: total}
		})
		if err != nil {
			s.events <- Event{Type: EventError, Err: err}
			return
		}
		s.events <- Event{Type: EventFileDone, From: startFrame.From, Name: startFrame.Name, Path: savePath}
	}

	s.host.StartStreamHandler()

	mgr := discovery.NewManager(s.cfg.Nickname)
	mgr.OnPeerFound = func(p discovery.DiscoveredPeer) {
		if p.ID != "" && p.Nickname != "" {
			s.host.RememberNickname(p.ID, p.Nickname)
		}
		s.events <- Event{Type: EventPeerFound, Peer: bnet.PeerInfo{
			ID: p.ID, Nickname: p.Nickname, Addrs: p.Addrs, Source: p.Source,
		}}
	}
	mgr.OnPeerLost = func(p discovery.DiscoveredPeer) {
		s.events <- Event{Type: EventPeerLost, Peer: bnet.PeerInfo{ID: p.ID}}
	}
	s.host.AttachDiscovery(mgr)

	if stop, err := discovery.BrowseBonjour(ctx, mgr); err == nil {
		s.stops = append(s.stops, stop)
		if server, err := discovery.RegisterBonjour(s.cfg.Nickname, listenPort(s.host), hostFullAddrs(s.host)); err == nil {
			s.stops = append(s.stops, func() { server.Shutdown() })
		}
	}
	if stop, err := discovery.SearchSSDP(ctx, mgr); err == nil {
		s.stops = append(s.stops, stop)
		if advStop, err := discovery.AdvertiseSSDP(ctx, s.cfg.Nickname, listenPort(s.host)); err == nil {
			s.stops = append(s.stops, advStop)
		}
	}
	_ = discovery.ListenWSD(ctx, mgr)
	_ = discovery.SendWSDHello(ctx, s.cfg.Nickname, listenPort(s.host))
	go discovery.StartBLE(ctx, s.cfg.Nickname, mgr)

	<-ctx.Done()
	return s.Stop()
}

func (s *Service) Stop() error {
	for i := len(s.stops) - 1; i >= 0; i-- {
		s.stops[i]()
	}
	if s.host != nil {
		return s.host.Close()
	}
	return nil
}

func (s *Service) Send(ctx context.Context, req SendRequest) error {
	if req.File != "" {
		pr, pw := io.Pipe()
		go func() {
			err := transfer.WriteFile(req.File, s.cfg.Nickname, pw)
			pw.CloseWithError(err)
		}()
		stream, err := s.host.Libp2p.NewStream(ctx, req.To, proto.Protocol)
		if err != nil {
			return err
		}
		defer stream.Close()
		_, err = io.Copy(stream, pr)
		return err
	}

	frame := proto.Frame{Type: proto.FrameText, From: s.cfg.Nickname, Content: req.Content}
	return s.host.SendFrame(ctx, req.To, frame)
}

func listenPort(h *bnet.Host) int {
	for _, a := range h.Libp2p.Addrs() {
		if portStr, err := a.ValueForProtocol(maddr.P_TCP); err == nil {
			var port int
			fmt.Sscanf(portStr, "%d", &port)
			return port
		}
	}
	return 4001
}

func hostFullAddrs(h *bnet.Host) []multiaddr.Multiaddr {
	out := make([]multiaddr.Multiaddr, 0, len(h.Libp2p.Addrs()))
	suffix, _ := multiaddr.NewMultiaddr("/p2p/" + h.Libp2p.ID().String())
	for _, a := range h.Libp2p.Addrs() {
		if _, err := a.ValueForProtocol(maddr.P_TCP); err == nil {
			out = append(out, a.Encapsulate(suffix))
		}
	}
	return out
}
