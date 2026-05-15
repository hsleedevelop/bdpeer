package core

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hsleedevelop/bdpeer/internal/config"
	"github.com/hsleedevelop/bdpeer/internal/discovery"
	bnet "github.com/hsleedevelop/bdpeer/internal/net"
	"github.com/hsleedevelop/bdpeer/internal/proto"
	"github.com/hsleedevelop/bdpeer/internal/transfer"
	"github.com/hsleedevelop/bdpeer/internal/transport"
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
	EventReady        EventType = "ready"
	EventLog          EventType = "log"
)

type Event struct {
	Type      EventType
	Peer      bnet.PeerInfo
	From      string
	Content   string
	Name      string
	Size      int64
	Received  int64
	Total     int64
	Path      string
	Err       error
	LocalAddr string
}

type SendRequest struct {
	To      peer.ID
	Content string
	File    string
}

type Service struct {
	cfg      *config.Config
	cfgPath  string
	host     *bnet.Host
	recvDir  string
	stops    []func()
	events   chan Event
	registry *transport.Registry
	libp2pT  *transport.Libp2pTransport
	webrtcT  *transport.WebRTCTransport
	mgr      *discovery.Manager
}

func NewService(cfg *config.Config, cfgPath string) *Service {
	return &Service{
		cfg:      cfg,
		cfgPath:  cfgPath,
		events:   make(chan Event, 256),
		registry: transport.NewRegistry(),
		webrtcT:  transport.NewWebRTCTransport(),
	}
}

func (s *Service) Events() <-chan Event { return s.events }

func (s *Service) log(msg string) {
	select {
	case s.events <- Event{Type: EventLog, Content: msg}:
	default:
	}
}

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

	s.log("libp2p 시작 중...")
	s.host, err = bnet.NewHostWithIdentity(ctx, s.cfg.Nickname, priv)
	if err != nil {
		return fmt.Errorf("new host: %w", err)
	}
	s.libp2pT = transport.NewLibp2pTransport(s.host)
	localAddr := bnet.FirstTCPAddr(s.host)
	s.events <- Event{Type: EventReady, LocalAddr: localAddr}
	s.log("호스트 준비 완료 → " + localAddr)

	if s.recvDir == "" {
		if s.cfg.DataDir != "" {
			s.recvDir = filepath.Join(s.cfg.DataDir, "received")
		} else {
			home, _ := os.UserHomeDir()
			s.recvDir = filepath.Join(home, "Downloads", "bdpeer")
		}
	}
	_ = os.MkdirAll(s.recvDir, 0o755)

	s.host.OnLog = s.log

	s.host.OnConnected = func(id peer.ID) {
		peerKey := transport.PeerID(id.String())
		s.registry.Register(peerKey, s.libp2pT)
		connCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		stream, err := s.libp2pT.OpenStream(connCtx, peerKey)
		if err != nil {
			return
		}
		defer stream.Close()
		_ = transfer.WriteFrame(stream, proto.Frame{Type: proto.FrameHello, From: s.cfg.Nickname})
	}
	s.host.OnDisconnected = func(id peer.ID) {
		s.registry.Unregister(transport.PeerID(id.String()))
	}

	s.libp2pT.SetHandler(s.onInboundStream)
	s.webrtcT.SetHandler(s.onInboundStream)

	s.mgr = discovery.NewManager(s.cfg.Nickname)
	mgr := s.mgr
	mgr.OnPeerFound = func(p discovery.DiscoveredPeer) {
		if p.ID != "" && p.Nickname != "" {
			s.host.RememberNickname(p.ID, p.Nickname)
		}
		nick := p.Nickname
		if nick == "" {
			nick = p.ID.String()[:8] + "..."
		}
		s.log("[" + p.Source + "] 피어 발견: " + nick)
		s.events <- Event{Type: EventPeerFound, Peer: bnet.PeerInfo{
			ID: p.ID, Nickname: p.Nickname, Addrs: p.Addrs, Source: p.Source,
		}}
	}
	mgr.OnPeerLost = func(p discovery.DiscoveredPeer) {
		s.events <- Event{Type: EventPeerLost, Peer: bnet.PeerInfo{ID: p.ID}}
	}

	s.host.AttachDiscovery(mgr)

	s.log("━━━ 피어 탐색 시작 ━━━")

	// [1/4] mDNS — 동일 LAN
	s.log("[1/4] mDNS/Bonjour (동일 LAN) 시작...")
	if stop, err := discovery.BrowseBonjour(ctx, mgr); err == nil {
		s.stops = append(s.stops, stop)
		if server, err := discovery.RegisterBonjour(s.cfg.Nickname, listenPort(s.host), hostFullAddrs(s.host)); err == nil {
			s.stops = append(s.stops, func() { server.Shutdown() })
			s.log("    ✓ mDNS 등록 완료")
		} else {
			s.log("    ✗ mDNS 등록 실패: " + err.Error())
		}
	} else {
		s.log("    ✗ mDNS 실패: " + err.Error())
	}

	// [2/4] SSDP/WSD — 동일 LAN (Windows 호환)
	s.log("[2/4] SSDP/WSD (동일 LAN, Windows) 시작...")
	if stop, err := discovery.SearchSSDP(ctx, mgr); err == nil {
		s.stops = append(s.stops, stop)
		if advStop, err := discovery.AdvertiseSSDP(ctx, s.cfg.Nickname, listenPort(s.host)); err == nil {
			s.stops = append(s.stops, advStop)
			s.log("    ✓ SSDP 광고 완료")
		}
	}
	_ = discovery.ListenWSD(ctx, mgr)
	_ = discovery.SendWSDHello(ctx, s.cfg.Nickname, listenPort(s.host))

	// [3/4] BLE — 근거리 크로스망 (~10m), darwin: BLE+WebRTC upgrade
	s.log("[3/4] BLE (근거리 ~10m) 시작...")
	go s.startBLEWithWebRTC(ctx, mgr)

	// [4/4] DHT — 인터넷
	s.log("[4/4] DHT (인터넷) 시작... (약 10초 후 광고)")
	s.host.StartDHTDiscovery(ctx, mgr)

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

// onInboundStream is the unified inbound handler for all transports.
// It reads frames from stream until EOF and dispatches each frame to the
// Service event channel. Currently the from PeerID is informational; frame
// dispatching keys off frame.From (sender's nickname) and frame.Type.
func (s *Service) onInboundStream(from transport.PeerID, stream io.ReadWriteCloser) {
	defer stream.Close()
	r := bufio.NewReader(stream)
	for {
		frame, err := transfer.ReadFrame(r)
		if err != nil {
			return
		}
		switch frame.Type {
		case proto.FrameText:
			s.events <- Event{Type: EventTextReceived, From: frame.From, Content: frame.Content}

		case proto.FrameHello:
			if frame.From == "" {
				continue
			}
			if pid, err := peer.Decode(string(from)); err == nil {
				// libp2p peer.
				s.host.RememberNickname(pid, frame.From)
				addrs := s.host.Libp2p.Peerstore().Addrs(pid)
				s.log("닉네임 수신: " + frame.From + " (" + pid.String()[:8] + "...)")
				s.events <- Event{Type: EventPeerFound, Peer: bnet.PeerInfo{
					ID: pid, Nickname: frame.From, Addrs: addrs, Source: "dht",
				}}
			} else if strings.HasPrefix(string(from), "ble-") && s.mgr != nil {
				// BLE peer whose nickname was unknown at connect time —
				// propagate via the discovery Manager so OnPeerFound emits EventPeerFound.
				peerUUID := strings.TrimPrefix(string(from), "ble-")
				s.mgr.Notify(discovery.DiscoveredPeer{
					ID:       peer.ID(string(from)),
					Nickname: frame.From,
					Addr:     peerUUID,
					Source:   "ble→webrtc",
				})
			}

		case proto.FrameFileStart:
			s.events <- Event{Type: EventFileStart, From: frame.From, Name: frame.Name, Size: frame.Size}
			var startBuf bytes.Buffer
			_ = transfer.WriteFrame(&startBuf, frame)
			combined := io.MultiReader(&startBuf, r)
			savePath, err := transfer.ReadFile(combined, s.recvDir, func(recv, total int64) {
				s.events <- Event{Type: EventFileProgress, From: frame.From, Received: recv, Total: total}
			})
			if err != nil {
				s.events <- Event{Type: EventError, Err: err}
				return
			}
			s.events <- Event{Type: EventFileDone, From: frame.From, Name: frame.Name, Path: savePath}
			return // file stream consumed the rest of this logical stream
		}
	}
}

func (s *Service) Connect(ctx context.Context, addr string) error {
	if s.host == nil {
		return fmt.Errorf("service not started")
	}
	_, err := s.host.ConnectByAddr(ctx, addr)
	return err
}

func (s *Service) ConnectByNickname(ctx context.Context, nickname string) error {
	if s.host == nil {
		return fmt.Errorf("service not started")
	}
	s.log("닉네임 검색 중: " + nickname + " ...")
	pi, err := s.host.FindByNickname(ctx, nickname)
	if err != nil {
		return err
	}
	s.log("발견: " + pi.ID.String()[:8] + "... 연결 중")
	return s.host.Libp2p.Connect(ctx, pi)
}

func (s *Service) Send(ctx context.Context, req SendRequest) error {
	peerKey := transport.PeerID(string(req.To))
	t, ok := s.registry.Lookup(peerKey)
	if !ok {
		return fmt.Errorf("unknown peer: %s", peerKey)
	}

	stream, err := t.OpenStream(ctx, peerKey)
	if err != nil {
		if errors.Is(err, transport.ErrNoConnection) {
			s.registry.Unregister(peerKey)
		}
		return err
	}
	defer stream.Close()

	if req.File != "" {
		pr, pw := io.Pipe()
		go func() {
			err := transfer.WriteFile(req.File, s.cfg.Nickname, pw)
			pw.CloseWithError(err)
		}()
		_, err = io.Copy(stream, pr)
		return err
	}

	return transfer.WriteFrame(stream, proto.Frame{
		Type: proto.FrameText, From: s.cfg.Nickname, Content: req.Content,
	})
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
