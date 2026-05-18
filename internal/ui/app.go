package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/hsleedevelop/bdpeer/internal/core"
	bnet "github.com/hsleedevelop/bdpeer/internal/net"
	"github.com/libp2p/go-libp2p/core/peer"
)

type Screen int

const (
	ScreenSetup Screen = iota
	ScreenMain
)

type MsgPeerFound struct{ Info bnet.PeerInfo }
type MsgPeerLost struct{ ID peer.ID }
type MsgTextReceived struct{ From, Content string }
type MsgFileStart struct {
	From, Name string
	Size       int64
}
type MsgFileProgress struct {
	From            string
	Received, Total int64
}
type MsgFileDone struct{ From, Name, SavePath string }
type MsgError struct{ Err error }
type MsgLocalAddr struct{ Addr string }
type MsgLog struct{ Text string }

type Message struct {
	From    string
	Content string
	Mine    bool
}

type fileXfer struct {
	From     string
	Name     string
	Received int64
	Total    int64
	Done     bool
	SavePath string
}

type Model struct {
	screen     Screen
	nickname   string
	localAddr  string
	peers      []bnet.PeerInfo
	activePeer *bnet.PeerInfo
	messages   []Message
	logs       []string
	showLog    bool
	inputBuf   string
	width      int
	height     int
	err        error
	fileXfer   *fileXfer
	sendCh     chan<- core.SendRequest
	nickCh     chan<- string
	connectCh  chan<- string
}

func New(nickname string) Model {
	screen := ScreenMain
	if nickname == "" {
		screen = ScreenSetup
	}
	return Model{screen: screen, nickname: nickname, showLog: true}
}

func NewWithChannels(nickname string, sendCh chan<- core.SendRequest, nickCh chan<- string, connectCh chan<- string) Model {
	m := New(nickname)
	m.sendCh = sendCh
	m.nickCh = nickCh
	m.connectCh = connectCh
	return m
}

func (m Model) Init() tea.Cmd { return nil }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		return m.handleKey(msg)
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case MsgPeerFound:
		m.peers = appendOrUpdate(m.peers, msg.Info)
	case MsgPeerLost:
		m.peers = removePeer(m.peers, msg.ID)
	case MsgTextReceived:
		m.messages = append(m.messages, Message{From: msg.From, Content: msg.Content})
	case MsgFileStart:
		m.fileXfer = &fileXfer{From: msg.From, Name: msg.Name, Total: msg.Size}
	case MsgFileProgress:
		if m.fileXfer != nil {
			m.fileXfer.Received = msg.Received
		}
	case MsgFileDone:
		if m.fileXfer != nil {
			m.fileXfer.Done = true
			m.fileXfer.SavePath = msg.SavePath
		}
	case MsgError:
		m.err = msg.Err
	case MsgLocalAddr:
		m.localAddr = msg.Addr
	case MsgLog:
		m.logs = append(m.logs, msg.Text)
		if len(m.logs) > 200 {
			m.logs = m.logs[len(m.logs)-200:]
		}
	}
	return m, nil
}

func (m Model) View() string {
	switch m.screen {
	case ScreenSetup:
		return setupView(m)
	default:
		return mainView(m)
	}
}

func mainView(m Model) string {
	if m.width == 0 {
		return "loading..."
	}
	leftW := 22
	rightW := m.width - leftW - 4

	left := peerListView(m, leftW, m.height-2)

	var right string
	if m.showLog {
		right = logView(m, rightW, m.height-2)
	} else {
		right = chatView(m, rightW, m.height-2)
	}

	addrHint := ""
	if m.localAddr != "" {
		addrHint = "  " + m.localAddr
	}
	logToggle := "  [Tab: 로그]"
	if m.showLog {
		logToggle = "  [Tab: 채팅]"
	}
	title := lipgloss.NewStyle().
		Width(m.width).
		Foreground(colorPrimary).
		Bold(true).
		Render(fmt.Sprintf(" bdpeer  [%s]%s%s", m.nickname, addrHint, logToggle))

	row := lipgloss.JoinHorizontal(lipgloss.Top, left, right)
	return title + "\n" + row
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.screen == ScreenSetup {
		return m.handleSetupKey(msg)
	}
	return m.handleMainKey(msg)
}

func (m Model) handleMainKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyCtrlC, tea.KeyEsc:
		return m, tea.Quit
	case tea.KeyTab:
		m.showLog = !m.showLog
		return m, nil
	case tea.KeyUp:
		m.activePeer = prevPeer(m.peers, m.activePeer)
	case tea.KeyDown:
		m.activePeer = nextPeer(m.peers, m.activePeer)
	case tea.KeyEnter:
		content := strings.TrimSpace(m.inputBuf)
		// Empty Enter in log view: jump to chat with the selected peer.
		// Auto-select the first peer if none is highlighted yet.
		if content == "" && m.showLog {
			if m.activePeer == nil && len(m.peers) > 0 {
				p := m.peers[0]
				m.activePeer = &p
			}
			if m.activePeer != nil {
				m.showLog = false
			}
			return m, nil
		}
		if content == "" {
			return m, nil
		}
		if strings.HasPrefix(content, "/connect ") {
			addr := strings.TrimSpace(strings.TrimPrefix(content, "/connect "))
			if m.connectCh != nil && addr != "" {
				select {
				case m.connectCh <- addr:
				default:
				}
			}
			m.inputBuf = ""
			return m, nil
		}
		if m.activePeer == nil || m.activePeer.ID == "" {
			return m, nil
		}
		if strings.HasPrefix(content, "/file ") {
			path := strings.TrimSpace(strings.TrimPrefix(content, "/file "))
			if m.sendCh != nil {
				select {
				case m.sendCh <- core.SendRequest{To: m.activePeer.ID, File: path}:
				default:
				}
			}
		} else {
			m.messages = append(m.messages, Message{From: m.nickname, Content: content, Mine: true})
			if m.sendCh != nil {
				select {
				case m.sendCh <- core.SendRequest{To: m.activePeer.ID, Content: content}:
				default:
				}
			}
		}
		m.inputBuf = ""
	case tea.KeySpace:
		m.inputBuf += " "
	case tea.KeyBackspace:
		if len(m.inputBuf) > 0 {
			m.inputBuf = m.inputBuf[:len(m.inputBuf)-1]
		}
	default:
		if msg.Type == tea.KeyRunes {
			if msg.String() == "q" && m.inputBuf == "" {
				return m, tea.Quit
			}
			m.inputBuf += string(msg.Runes)
		}
	}
	return m, nil
}

func appendOrUpdate(peers []bnet.PeerInfo, info bnet.PeerInfo) []bnet.PeerInfo {
	for i, p := range peers {
		if samePeer(p, info) {
			peers[i] = info
			return peers
		}
	}
	return append(peers, info)
}

func removePeer(peers []bnet.PeerInfo, id peer.ID) []bnet.PeerInfo {
	out := peers[:0]
	for _, p := range peers {
		if p.ID != id {
			out = append(out, p)
		}
	}
	return out
}

func samePeer(a, b bnet.PeerInfo) bool {
	if a.ID != "" && b.ID != "" {
		return a.ID == b.ID
	}
	if len(a.Addrs) > 0 && len(b.Addrs) > 0 {
		return a.Addrs[0].String() == b.Addrs[0].String()
	}
	return false
}

func prevPeer(peers []bnet.PeerInfo, active *bnet.PeerInfo) *bnet.PeerInfo {
	if len(peers) == 0 {
		return nil
	}
	if active == nil {
		p := peers[len(peers)-1]
		return &p
	}
	for i, p := range peers {
		if p.ID == active.ID && i > 0 {
			prev := peers[i-1]
			return &prev
		}
	}
	return active
}

func nextPeer(peers []bnet.PeerInfo, active *bnet.PeerInfo) *bnet.PeerInfo {
	if len(peers) == 0 {
		return nil
	}
	if active == nil {
		p := peers[0]
		return &p
	}
	for i, p := range peers {
		if p.ID == active.ID && i < len(peers)-1 {
			next := peers[i+1]
			return &next
		}
	}
	return active
}
