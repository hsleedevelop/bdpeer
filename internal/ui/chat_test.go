package ui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestMainViewDoesNotExceedWindowHeightWithLongLogs(t *testing.T) {
	m := New("chad")
	m.width = 80
	m.height = 12

	longLog := "DHT 연결 실패: 12D3KooW... — failed to dial: " + strings.Repeat("/ip4/192.168.0.10/tcp/4001/p2p/12D3KooWPeerAddress ", 6)
	for range 30 {
		m.logs = append(m.logs, longLog)
	}

	got := mainView(m)
	if h := lipgloss.Height(got); h > m.height {
		t.Fatalf("mainView height = %d, want <= %d\n%s", h, m.height, got)
	}
}

func TestUpdateCapsLogsAt200Entries(t *testing.T) {
	m := New("chad")

	for i := range 250 {
		next, _ := m.Update(MsgLog{Text: fmt.Sprintf("log-%03d", i)})
		updated, ok := next.(Model)
		if !ok {
			t.Fatalf("Update returned %T, want ui.Model", next)
		}
		m = updated
	}

	if len(m.logs) != 200 {
		t.Fatalf("len(logs) = %d, want 200", len(m.logs))
	}
	if m.logs[0] != "log-050" {
		t.Fatalf("first log = %q, want log-050", m.logs[0])
	}
	if m.logs[len(m.logs)-1] != "log-249" {
		t.Fatalf("last log = %q, want log-249", m.logs[len(m.logs)-1])
	}
}
