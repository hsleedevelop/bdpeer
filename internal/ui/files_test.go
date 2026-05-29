package ui

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/hsleedevelop/bdpeer/internal/core"
	bnet "github.com/hsleedevelop/bdpeer/internal/net"
	"github.com/libp2p/go-libp2p/core/peer"
)

func keyRunes(s string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func keyType(t tea.KeyType) tea.KeyMsg {
	return tea.KeyMsg{Type: t}
}

func updateFilesModel(t *testing.T, m Model, msg tea.KeyMsg) Model {
	t.Helper()
	next, _ := m.handleFilesKey(msg)
	updated, ok := next.(Model)
	if !ok {
		t.Fatalf("handleFilesKey returned %T, want ui.Model", next)
	}
	return updated
}

func TestVisibleFileEntriesFiltersCaseInsensitiveAndKeepsParent(t *testing.T) {
	entries := []fileEntry{
		{Name: "..", IsDir: true},
		{Name: "Alpha.txt"},
		{Name: "beta.go"},
		{Name: "Docs", IsDir: true},
	}

	got := visibleFileEntries(entries, "AL")

	want := []fileEntry{
		{Name: "..", IsDir: true},
		{Name: "Alpha.txt"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("visibleFileEntries() = %#v, want %#v", got, want)
	}
}

func TestHandleFilesKeyFiltersAndSelectsVisibleFile(t *testing.T) {
	peer := bnet.PeerInfo{Nickname: "peer"}
	m := Model{
		focus:   focusFiles,
		fileCwd: filepath.Join("tmp", "bdpeer"),
		fileEntries: []fileEntry{
			{Name: "alpha.txt"},
			{Name: "beta.txt"},
		},
		activePeer: &peer,
	}

	for _, msg := range []tea.KeyMsg{
		keyRunes("/"),
		keyRunes("a"),
		keyRunes("l"),
		keyRunes("p"),
		keyType(tea.KeyEnter),
		keyType(tea.KeyEnter),
	} {
		m = updateFilesModel(t, m, msg)
	}

	want := filepath.Join("tmp", "bdpeer", "alpha.txt")
	if m.confirmPath != want {
		t.Fatalf("confirmPath = %q, want %q", m.confirmPath, want)
	}
}

func TestCachedFilesViewInvalidatesWhenConfirmPathChanges(t *testing.T) {
	peer := bnet.PeerInfo{Nickname: "peer"}
	m := Model{
		focus:   focusFiles,
		fileCwd: filepath.Join("tmp", "bdpeer"),
		fileEntries: []fileEntry{
			{Name: "alpha.txt"},
		},
		activePeer: &peer,
		cache:      &viewCache{},
	}

	first := m.cachedFilesView(40, 12)
	if strings.Contains(first, "전송하시겠습니까?") {
		t.Fatal("initial files view unexpectedly rendered confirmation prompt")
	}

	m.confirmPath = filepath.Join("tmp", "bdpeer", "alpha.txt")
	second := m.cachedFilesView(40, 12)
	if !strings.Contains(second, "전송하시겠습니까?") {
		t.Fatal("files view did not refresh to render confirmation prompt")
	}
}

func TestHandleMainKeyEnterConfirmsFileSend(t *testing.T) {
	sendCh := make(chan core.SendRequest, 1)
	peerInfo := bnet.PeerInfo{ID: peer.ID("ble-session"), Nickname: "peer", Source: "ble"}
	m := Model{
		screen:      ScreenMain,
		focus:       focusFiles,
		activePeer:  &peerInfo,
		sendCh:      sendCh,
		confirmPath: filepath.Join("tmp", "bdpeer", "alpha.txt"),
	}

	next, _ := m.handleMainKey(keyType(tea.KeyEnter))
	updated, ok := next.(Model)
	if !ok {
		t.Fatalf("handleMainKey returned %T, want ui.Model", next)
	}
	if updated.confirmPath != "" {
		t.Fatalf("confirmPath = %q, want empty", updated.confirmPath)
	}

	select {
	case req := <-sendCh:
		if req.File != filepath.Join("tmp", "bdpeer", "alpha.txt") {
			t.Fatalf("req.File = %q, want selected file", req.File)
		}
	case <-time.After(time.Second):
		t.Fatal("expected file send request")
	}
}

func TestHandleFilesKeySlashReopensFilterWithoutClearingQuery(t *testing.T) {
	m := Model{
		focus: focusFiles,
		fileEntries: []fileEntry{
			{Name: "alpha.txt"},
			{Name: "beta.txt"},
		},
		fileFilter: "alp",
		fileIdx:    1,
	}

	m = updateFilesModel(t, m, keyRunes("/"))
	if !m.fileFiltering {
		t.Fatal("fileFiltering = false, want true")
	}
	if m.fileFilter != "alp" {
		t.Fatalf("fileFilter = %q, want %q", m.fileFilter, "alp")
	}
	if m.fileIdx != 0 {
		t.Fatalf("fileIdx = %d, want 0", m.fileIdx)
	}

	m = updateFilesModel(t, m, keyRunes("h"))
	if m.fileFilter != "alph" {
		t.Fatalf("fileFilter after edit = %q, want %q", m.fileFilter, "alph")
	}
}

func TestHandleFilesKeyEscClearsAppliedFilter(t *testing.T) {
	m := Model{
		focus:      focusFiles,
		fileFilter: "alp",
		fileIdx:    1,
	}

	m = updateFilesModel(t, m, keyType(tea.KeyEsc))

	if m.fileFilter != "" {
		t.Fatalf("fileFilter = %q, want empty", m.fileFilter)
	}
	if m.fileIdx != 0 {
		t.Fatalf("fileIdx = %d, want 0", m.fileIdx)
	}
}

func TestPeerListViewUsesEnglishSelfCommandsAndVersion(t *testing.T) {
	m := New("chad").WithVersion("v1.2.3")
	m.focus = focusPeers

	got := peerListView(m, 32, 16)

	for _, want := range []string{
		"Me: chad",
		"Version: v1.2.3",
		"Commands:",
		"→ Chat",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("peerListView() missing %q:\n%s", want, got)
		}
	}
	for _, old := range []string{"나:", "채팅"} {
		if strings.Contains(got, old) {
			t.Fatalf("peerListView() still contains %q:\n%s", old, got)
		}
	}
}

func TestFileTreeViewUsesEnglishFilterLabels(t *testing.T) {
	m := Model{
		focus:         focusFiles,
		fileFilter:    "alp",
		fileFiltering: true,
		fileEntries: []fileEntry{
			{Name: "alpha.txt"},
			{Name: "beta.txt"},
		},
	}

	got := fileTreeView(m, 80, 14)

	for _, want := range []string{
		"Filter: alp",
		"Type filter",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("fileTreeView() missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "필터") {
		t.Fatalf("fileTreeView() still contains Korean filter text:\n%s", got)
	}
}

func TestFocusBarUsesEnglishLabels(t *testing.T) {
	got := focusBar(focusChat)

	for _, want := range []string{"[1.Peers]", "[2.Chat]", "[3.Files]"} {
		if !strings.Contains(got, want) {
			t.Fatalf("focusBar() missing %q: %s", want, got)
		}
	}
	if strings.Contains(got, "채팅") {
		t.Fatalf("focusBar() still contains Korean chat text: %s", got)
	}
}

func TestMainViewRendersAppVersion(t *testing.T) {
	m := New("chad").WithVersion("1.2.3")
	m.width = 80
	m.height = 20

	got := mainView(m)

	if !strings.Contains(got, "bdpeer v1.2.3") {
		t.Fatalf("mainView() did not render app version:\n%s", got)
	}
}

func TestSetupViewRendersAppVersion(t *testing.T) {
	m := New("").WithVersion("1.2.3")
	m.width = 80
	m.height = 20

	got := setupView(m)

	if !strings.Contains(got, "bdpeer v1.2.3") {
		t.Fatalf("setupView() did not render app version:\n%s", got)
	}
}

func TestDisplayVersionNormalizesReleaseVersions(t *testing.T) {
	tests := map[string]string{
		"1.2.3":  "v1.2.3",
		"v1.2.3": "v1.2.3",
		"dev":    "dev",
		"":       "",
	}
	for input, want := range tests {
		if got := DisplayVersion(input); got != want {
			t.Fatalf("DisplayVersion(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestHandleMainKeyDoesNotConsumeChatRunesAsShortcuts(t *testing.T) {
	m := Model{screen: ScreenMain, focus: focusChat}

	next, _ := m.handleMainKey(keyRunes("s"))
	updated, ok := next.(Model)
	if !ok {
		t.Fatalf("handleMainKey returned %T, want ui.Model", next)
	}
	if updated.inputBuf != "s" {
		t.Fatalf("inputBuf = %q, want s", updated.inputBuf)
	}
}

func TestHandlePeersKeyDoesNotUseSForBLESearch(t *testing.T) {
	m := Model{screen: ScreenMain, focus: focusPeers}

	next, _ := m.handlePeersKey(keyRunes("s"))
	updated, ok := next.(Model)
	if !ok {
		t.Fatalf("handlePeersKey returned %T, want ui.Model", next)
	}
	if updated.focus != focusPeers {
		t.Fatalf("focus = %v, want peers", updated.focus)
	}
}
