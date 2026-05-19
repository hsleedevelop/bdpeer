package ui

import (
	"fmt"
	"strings"
)

func progressBar(received, total int64, width int) (string, int) {
	if width < 2 {
		width = 2
	}
	if total <= 0 {
		return "[" + strings.Repeat("░", width-2) + "]", 0
	}
	ratio := float64(received) / float64(total)
	if ratio > 1 {
		ratio = 1
	}
	pct := int(ratio * 100)
	inner := width - 2
	filled := int(ratio * float64(inner))
	if filled > inner {
		filled = inner
	}
	return "[" + strings.Repeat("█", filled) + strings.Repeat("░", inner-filled) + "]", pct
}

func logView(m Model, width, height int) string {
	header := StyleTitle.Render("Log")
	lineHeight := height - 3
	start := 0
	if len(m.logs) > lineHeight {
		start = len(m.logs) - lineHeight
	}
	var lines []string
	for _, l := range m.logs[start:] {
		lines = append(lines, StyleHelp.Render(l))
	}
	help := StyleHelp.Render("↑/↓ 선택  Enter/Tab 채팅으로  /connect <addr> 수동 연결")
	body := header + "\n" + strings.Join(lines, "\n") + "\n" + help
	return panelStyle(m.focus == focusChat).Width(width).Height(height).Render(body)
}

func chatView(m Model, width, height int) string {
	if m.activePeer == nil {
		empty := StyleHelp.Render("Select a peer to start chatting")
		return panelStyle(m.focus == focusChat).Width(width).Height(height).Render(empty)
	}

	header := StyleTitle.Render("Chat: " + m.activePeer.Nickname)

	msgHeight := height - 6
	var msgs []string
	start := 0
	if len(m.messages) > msgHeight {
		start = len(m.messages) - msgHeight
	}
	for _, msg := range m.messages[start:] {
		var line string
		if msg.Mine {
			line = StyleMessageMine.Render("you: ") + msg.Content
		} else {
			line = StyleMessage.Render(msg.From+": ") + msg.Content
		}
		msgs = append(msgs, line)
	}

	msgArea := strings.Join(msgs, "\n")
	input := StyleInput.Width(width - 4).Render("> " + m.inputBuf)

	var xfer string
	if m.fileXfer != nil {
		barW := width - 12
		if barW < 10 {
			barW = 10
		}
		verb := "Receiving"
		if m.fileXfer.Outgoing {
			verb = "Sending"
		}
		if !m.fileXfer.Done {
			bar, pct := progressBar(m.fileXfer.Received, m.fileXfer.Total, barW)
			xfer = StyleHelp.Render(fmt.Sprintf("%s %s", verb, m.fileXfer.Name)) + "\n" +
				StyleMessageMine.Render(bar) + StyleHelp.Render(fmt.Sprintf(" %d%%", pct)) + "\n"
		} else {
			if m.fileXfer.Outgoing {
				xfer = StyleHelp.Render(fmt.Sprintf("✓ %s 전송 완료", m.fileXfer.Name)) + "\n"
			} else {
				xfer = StyleHelp.Render(fmt.Sprintf("✓ %s saved to %s", m.fileXfer.Name, m.fileXfer.SavePath)) + "\n"
			}
		}
	}

	help := StyleHelp.Render("Enter send  /file <path> send file  Esc quit")
	body := header + "\n" + msgArea + "\n" + xfer + input + "\n" + help
	return panelStyle(m.focus == focusChat).Width(width).Height(height).Render(body)
}
