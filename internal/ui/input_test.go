package ui

import (
	"testing"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	bnet "github.com/hsleedevelop/bdpeer/internal/net"
	"github.com/libp2p/go-libp2p/core/peer"
)

func TestHandleMainKeyBackspaceRemovesFullUTF8Rune(t *testing.T) {
	m := Model{screen: ScreenMain, focus: focusChat, inputBuf: "안녕"}

	next, _ := m.handleMainKey(tea.KeyMsg{Type: tea.KeyBackspace})
	updated, ok := next.(Model)
	if !ok {
		t.Fatalf("handleMainKey returned %T, want ui.Model", next)
	}

	if updated.inputBuf != "안" {
		t.Fatalf("inputBuf = %q, want %q", updated.inputBuf, "안")
	}
	if !utf8.ValidString(updated.inputBuf) {
		t.Fatalf("inputBuf is not valid UTF-8: %q", updated.inputBuf)
	}
}

func TestHandleMainKeyBackspaceDropsTrailingInvalidUTF8Byte(t *testing.T) {
	m := Model{screen: ScreenMain, focus: focusChat, inputBuf: "한\xe1"}

	next, _ := m.handleMainKey(tea.KeyMsg{Type: tea.KeyBackspace})
	updated, ok := next.(Model)
	if !ok {
		t.Fatalf("handleMainKey returned %T, want ui.Model", next)
	}

	if updated.inputBuf != "한" {
		t.Fatalf("inputBuf = %q, want %q", updated.inputBuf, "한")
	}
	if !utf8.ValidString(updated.inputBuf) {
		t.Fatalf("inputBuf is not valid UTF-8: %q", updated.inputBuf)
	}
}

func TestHandleSetupKeyBackspaceRemovesFullUTF8Rune(t *testing.T) {
	m := Model{screen: ScreenSetup, inputBuf: "채드"}

	next, _ := m.handleSetupKey(tea.KeyMsg{Type: tea.KeyBackspace})
	updated, ok := next.(Model)
	if !ok {
		t.Fatalf("handleSetupKey returned %T, want ui.Model", next)
	}

	if updated.inputBuf != "채" {
		t.Fatalf("inputBuf = %q, want %q", updated.inputBuf, "채")
	}
	if !utf8.ValidString(updated.inputBuf) {
		t.Fatalf("inputBuf is not valid UTF-8: %q", updated.inputBuf)
	}
}

func TestCachedChatBodyIgnoresInputBufferChanges(t *testing.T) {
	peerInfo := bnet.PeerInfo{ID: peer.ID("peer-id"), Nickname: "peer"}
	m := Model{
		screen:     ScreenMain,
		focus:      focusChat,
		activePeer: &peerInfo,
		messages: []Message{
			{From: "peer", Content: "안녕하세요", Mine: false},
		},
		cache: &viewCache{},
	}

	first := m.cachedChatBody(50, 12)
	firstKey := m.cache.chatBodyKey
	m.inputBuf = "입력 중"
	second := m.cachedChatBody(50, 12)

	if second != first {
		t.Fatal("chat body cache changed when only inputBuf changed")
	}
	if m.cache.chatBodyKey != firstKey {
		t.Fatal("chat body cache key changed when only inputBuf changed")
	}
}
