package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/hsleedevelop/bdpeer/internal/core"
	bnet "github.com/hsleedevelop/bdpeer/internal/net"
	"github.com/libp2p/go-libp2p/core/peer"
)

type focusArea int

const (
	focusPeers focusArea = iota
	focusChat
	focusFiles
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
	Outgoing   bool
}
type MsgFileProgress struct {
	From            string
	Received, Total int64
	Outgoing        bool
}
type MsgFileDone struct {
	From, Name, SavePath string
	Outgoing             bool
}
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
	Outgoing bool
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

	focus       focusArea
	fileCwd     string
	fileEntries []fileEntry
	fileIdx     int
	confirmPath string

	// Hangul IME fires 2-3 key events per syllable. Re-rendering all three
	// panels through lipgloss on every keystroke is the dominant cost; cache
	// the peer and files panels and recompute only when their inputs change.
	// Shared via pointer so mutation persists across value-copied Models.
	cache *viewCache
}

type viewCache struct {
	peerKey, peerView   string
	filesKey, filesView string
}

func New(nickname string) Model {
	screen := ScreenMain
	if nickname == "" {
		screen = ScreenSetup
	}
	cwd, err := os.Getwd()
	if err != nil {
		cwd, _ = os.UserHomeDir()
	}
	return Model{
		screen:      screen,
		nickname:    nickname,
		showLog:     true,
		focus:       focusChat,
		fileCwd:     cwd,
		fileEntries: readDir(cwd),
		cache:       &viewCache{},
	}
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
		m.fileXfer = &fileXfer{From: msg.From, Name: msg.Name, Total: msg.Size, Outgoing: msg.Outgoing}
	case MsgFileProgress:
		if m.fileXfer != nil {
			m.fileXfer.Received = msg.Received
			m.fileXfer.Outgoing = msg.Outgoing
		}
	case MsgFileDone:
		if m.fileXfer != nil {
			m.fileXfer.Done = true
			m.fileXfer.SavePath = msg.SavePath
			m.fileXfer.Outgoing = msg.Outgoing
		}
	case MsgError:
		m.err = msg.Err
		if msg.Err != nil {
			m.logs = append(m.logs, "오류: "+msg.Err.Error())
		}
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
	filesW := 32
	midW := m.width - leftW - filesW - 6
	if midW < 20 {
		midW = 20
	}
	h := m.height - 2

	left := m.cachedPeerView(leftW, h)

	var mid string
	if m.showLog {
		mid = logView(m, midW, h)
	} else {
		mid = chatView(m, midW, h)
	}

	right := m.cachedFilesView(filesW, h)

	addrHint := ""
	if m.localAddr != "" {
		addrHint = "  " + m.localAddr
	}
	logToggle := "  [Tab: 로그]"
	if m.showLog {
		logToggle = "  [Tab: 채팅]"
	}
	focusHint := "  " + focusBar(m.focus)
	title := lipgloss.NewStyle().
		Width(m.width).
		Foreground(colorPrimary).
		Bold(true).
		Render(fmt.Sprintf(" bdpeer  [%s]%s%s%s", m.nickname, addrHint, logToggle, focusHint))

	row := lipgloss.JoinHorizontal(lipgloss.Top, left, mid, right)
	return title + "\n" + row
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.screen == ScreenSetup {
		return m.handleSetupKey(msg)
	}
	return m.handleMainKey(msg)
}

func (m Model) handleMainKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.confirmPath != "" {
		switch strings.ToLower(msg.String()) {
		case "y":
			path := m.confirmPath
			m.confirmPath = ""
			if m.activePeer != nil && m.sendCh != nil {
				select {
				case m.sendCh <- core.SendRequest{To: m.activePeer.ID, File: path}:
				default:
				}
			}
		case "n", "esc":
			m.confirmPath = ""
		}
		return m, nil
	}

	if msg.Type == tea.KeyCtrlC {
		return m, tea.Quit
	}

	switch m.focus {
	case focusFiles:
		return m.handleFilesKey(msg)
	case focusPeers:
		return m.handlePeersKey(msg)
	}

	// focusChat: pre-check panel shortcuts when input is empty.
	if m.inputBuf == "" && msg.Type == tea.KeyRunes {
		switch msg.String() {
		case "1":
			m.focus = focusPeers
			return m, nil
		case "3":
			m.focus = focusFiles
			return m, nil
		}
	}

	switch msg.Type {
	case tea.KeyEsc:
		return m, tea.Quit
	case tea.KeyRight:
		m.focus = focusFiles
		return m, nil
	case tea.KeyLeft:
		m.focus = focusPeers
		return m, nil
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
		if m.activePeer == nil {
			m.logs = append(m.logs, "전송 불가: 활성 피어 없음")
			m.messages = append(m.messages, Message{From: "system", Content: "전송 불가: 활성 피어 없음"})
			m.inputBuf = ""
			return m, nil
		}
		// Re-resolve activePeer against m.peers so that newly-discovered IDs
		// (e.g. DHT resolves nickname after BLE/SSDP) are picked up before send.
		for _, p := range m.peers {
			if samePeer(p, *m.activePeer) {
				fresh := p
				m.activePeer = &fresh
				break
			}
		}
		if m.activePeer.ID == "" {
			warn := "전송 불가: '" + m.activePeer.Nickname + "' 의 ID 미확정 — 잠시 후 다시 시도하거나 /connect 사용"
			m.logs = append(m.logs, warn)
			m.messages = append(m.messages, Message{From: "system", Content: warn})
			m.inputBuf = ""
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

func (m Model) handlePeersKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		return m, tea.Quit
	case tea.KeyUp:
		m.activePeer = prevPeer(m.peers, m.activePeer)
	case tea.KeyDown:
		m.activePeer = nextPeer(m.peers, m.activePeer)
	case tea.KeyTab:
		m.showLog = !m.showLog
	case tea.KeyRight:
		m.focus = focusChat
	case tea.KeyEnter:
		if m.activePeer == nil && len(m.peers) > 0 {
			p := m.peers[0]
			m.activePeer = &p
		}
		if m.activePeer != nil {
			m.showLog = false
		}
		m.focus = focusChat
	case tea.KeyRunes:
		switch msg.String() {
		case "2":
			m.focus = focusChat
		case "3":
			m.focus = focusFiles
		case "q":
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m Model) handleFilesKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.Type == tea.KeyRunes {
		switch msg.String() {
		case "1":
			m.focus = focusPeers
			return m, nil
		case "2":
			m.focus = focusChat
			return m, nil
		}
	}
	switch msg.Type {
	case tea.KeyEsc:
		return m, tea.Quit
	case tea.KeyLeft:
		m.focus = focusChat
		return m, nil
	case tea.KeyUp:
		if m.fileIdx > 0 {
			m.fileIdx--
		}
	case tea.KeyDown:
		if m.fileIdx < len(m.fileEntries)-1 {
			m.fileIdx++
		}
	case tea.KeyEnter:
		if m.fileIdx < 0 || m.fileIdx >= len(m.fileEntries) {
			return m, nil
		}
		e := m.fileEntries[m.fileIdx]
		full := filepath.Join(m.fileCwd, e.Name)
		if e.IsDir {
			if e.Name == ".." {
				full = filepath.Dir(m.fileCwd)
			}
			m.fileCwd = full
			m.fileEntries = readDir(full)
			m.fileIdx = 0
			return m, nil
		}
		if m.activePeer == nil {
			return m, nil
		}
		m.confirmPath = full
	}
	return m, nil
}

func appendOrUpdate(peers []bnet.PeerInfo, info bnet.PeerInfo) []bnet.PeerInfo {
	for i, p := range peers {
		if samePeer(p, info) {
			merged := info
			if merged.ID == "" {
				merged.ID = p.ID
			}
			if merged.Nickname == "" {
				merged.Nickname = p.Nickname
			}
			if len(merged.Addrs) == 0 {
				merged.Addrs = p.Addrs
			}
			if merged.Source == "" {
				merged.Source = p.Source
			}
			peers[i] = merged
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
	// When one side lacks an ID (e.g. SSDP-only entry), fall back to nickname
	// so the entry can be promoted in-place once Hello arrives.
	if a.Nickname != "" && b.Nickname != "" && a.Nickname == b.Nickname {
		return true
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

// cachedPeerView returns the peer panel, recomputing only when its inputs
// change. The fingerprint covers everything peerListView reads: panel size,
// focus highlight, peer list contents, and active selection.
func (m Model) cachedPeerView(w, h int) string {
	if m.cache == nil {
		return peerListView(m, w, h)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%dx%d|f=%v|n=%d", w, h, m.focus == focusPeers, len(m.peers))
	for _, p := range m.peers {
		b.WriteByte('|')
		b.WriteString(string(p.ID))
		b.WriteByte(':')
		b.WriteString(p.Nickname)
		b.WriteByte(':')
		b.WriteString(p.Source)
	}
	b.WriteString("|a=")
	if m.activePeer != nil {
		b.WriteString(string(m.activePeer.ID))
	}
	key := b.String()
	if key == m.cache.peerKey && m.cache.peerView != "" {
		return m.cache.peerView
	}
	view := peerListView(m, w, h)
	m.cache.peerKey = key
	m.cache.peerView = view
	return view
}

// cachedFilesView returns the files panel, recomputing only when its inputs
// change.
func (m Model) cachedFilesView(w, h int) string {
	if m.cache == nil {
		return fileTreeView(m, w, h)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%dx%d|f=%v|cwd=%s|i=%d|n=%d", w, h, m.focus == focusFiles, m.fileCwd, m.fileIdx, len(m.fileEntries))
	for _, e := range m.fileEntries {
		b.WriteByte('|')
		b.WriteString(e.Name)
		if e.IsDir {
			b.WriteByte('/')
		}
	}
	key := b.String()
	if key == m.cache.filesKey && m.cache.filesView != "" {
		return m.cache.filesView
	}
	view := fileTreeView(m, w, h)
	m.cache.filesKey = key
	m.cache.filesView = view
	return view
}
