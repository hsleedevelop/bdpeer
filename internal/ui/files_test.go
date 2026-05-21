package ui

import (
	"path/filepath"
	"reflect"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	bnet "github.com/hsleedevelop/bdpeer/internal/net"
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

func TestHandleMainKeyDoesNotConsumeChatRunesAsShortcuts(t *testing.T) {
	searchCh := make(chan struct{}, 1)
	m := Model{screen: ScreenMain, focus: focusChat, bleSearchCh: searchCh}

	next, _ := m.handleMainKey(keyRunes("s"))
	updated, ok := next.(Model)
	if !ok {
		t.Fatalf("handleMainKey returned %T, want ui.Model", next)
	}
	if updated.inputBuf != "s" {
		t.Fatalf("inputBuf = %q, want s", updated.inputBuf)
	}

	select {
	case <-searchCh:
		t.Fatal("did not expect BLE search request while typing in chat")
	default:
	}
}

func TestHandlePeersKeyRequestsBLESearch(t *testing.T) {
	searchCh := make(chan struct{}, 1)
	m := Model{screen: ScreenMain, focus: focusPeers, bleSearchCh: searchCh}

	next, _ := m.handlePeersKey(keyRunes("s"))
	if _, ok := next.(Model); !ok {
		t.Fatalf("handlePeersKey returned %T, want ui.Model", next)
	}

	select {
	case <-searchCh:
	default:
		t.Fatal("expected BLE search request")
	}
}
