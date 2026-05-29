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
	lineHeight := height - 2
	if lineHeight < 0 {
		lineHeight = 0
	}
	start := 0
	if len(m.logs) > lineHeight {
		start = len(m.logs) - lineHeight
	}
	var lines []string
	for _, l := range m.logs[start:] {
		l = strings.ReplaceAll(l, "\n", " ")
		lines = append(lines, StyleHelp.Render(truncateForWidth(l, width)))
	}
	help := StyleHelp.Render(truncateForWidth("↑/↓ select  Enter/Tab Chat  /connect <addr>", width))
	body := header + "\n" + strings.Join(lines, "\n") + "\n" + help
	return panelStyle(m.focus == focusChat).Width(width).Height(height).Render(body)
}

func (m Model) cachedLogView(width, height int) string {
	if m.cache == nil {
		return logView(m, width, height)
	}
	key := m.logViewKey(width, height)
	if key == m.cache.logKey && m.cache.logView != "" {
		return m.cache.logView
	}
	view := logView(m, width, height)
	m.cache.logKey = key
	m.cache.logView = view
	return view
}

func (m Model) logViewKey(width, height int) string {
	lineHeight := height - 2
	if lineHeight < 0 {
		lineHeight = 0
	}
	start := 0
	if len(m.logs) > lineHeight {
		start = len(m.logs) - lineHeight
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%dx%d|f=%v|n=%d", width, height, m.focus == focusChat, len(m.logs))
	for _, l := range m.logs[start:] {
		fmt.Fprintf(&b, "|%q", l)
	}
	return b.String()
}

func chatView(m Model, width, height int) string {
	if m.activePeer == nil {
		empty := StyleHelp.Render("Select a peer to start chatting")
		return panelStyle(m.focus == focusChat).Width(width).Height(height).Render(empty)
	}

	inputWidth := width - 4
	if inputWidth < 1 {
		inputWidth = 1
	}
	body := m.cachedChatBody(width, height)
	input := StyleInput.Width(inputWidth).Render(inputLine(m.inputBuf, inputWidth))
	help := StyleHelp.Render("Enter send  /file <path> send file  Esc quit")
	body += input + "\n" + help
	return panelStyle(m.focus == focusChat).Width(width).Height(height).Render(body)
}

func (m Model) cachedChatBody(width, height int) string {
	if m.cache == nil {
		return chatBody(m, width, height)
	}
	key := m.chatBodyKey(width, height)
	if key == m.cache.chatBodyKey && m.cache.chatBodyView != "" {
		return m.cache.chatBodyView
	}
	view := chatBody(m, width, height)
	m.cache.chatBodyKey = key
	m.cache.chatBodyView = view
	return view
}

func (m Model) chatBodyKey(width, height int) string {
	msgHeight := height - 6
	start := 0
	if len(m.messages) > msgHeight {
		start = len(m.messages) - msgHeight
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%dx%d|n=%d", width, height, len(m.messages))
	if m.activePeer != nil {
		fmt.Fprintf(&b, "|peer=%q:%q", string(m.activePeer.ID), m.activePeer.Nickname)
	}
	if m.fileXfer != nil {
		fmt.Fprintf(
			&b,
			"|xfer=%q:%q:%d:%d:%v:%q:%v",
			m.fileXfer.From,
			m.fileXfer.Name,
			m.fileXfer.Received,
			m.fileXfer.Total,
			m.fileXfer.Done,
			m.fileXfer.SavePath,
			m.fileXfer.Outgoing,
		)
	}
	for _, msg := range m.messages[start:] {
		fmt.Fprintf(&b, "|msg=%q:%q:%v", msg.From, msg.Content, msg.Mine)
	}
	return b.String()
}

func chatBody(m Model, width, height int) string {
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
			// On the sender side `Received` tracks bytes written into the
			// transport buffer, not bytes the peer has actually consumed.
			// Once we've flushed everything, swap the bar for a "waiting on
			// receiver" hint so the sender doesn't confuse buffer-flush with
			// delivery — Done still requires the FILE_ACK round-trip.
			if m.fileXfer.Outgoing && m.fileXfer.Total > 0 && m.fileXfer.Received >= m.fileXfer.Total {
				xfer = StyleHelp.Render(fmt.Sprintf("✓ %s 송출 완료 · 수신 대기 중...", m.fileXfer.Name)) + "\n"
			} else {
				xfer = StyleHelp.Render(fmt.Sprintf("%s %s", verb, m.fileXfer.Name)) + "\n" +
					StyleMessageMine.Render(bar) + StyleHelp.Render(fmt.Sprintf(" %d%%", pct)) + "\n"
			}
		} else {
			if m.fileXfer.Outgoing {
				xfer = StyleHelp.Render(fmt.Sprintf("✓ %s 전송 완료", m.fileXfer.Name)) + "\n"
			} else {
				xfer = StyleHelp.Render(fmt.Sprintf("✓ %s saved to %s", m.fileXfer.Name, m.fileXfer.SavePath)) + "\n"
			}
		}
	}

	return header + "\n" + msgArea + "\n" + xfer
}
