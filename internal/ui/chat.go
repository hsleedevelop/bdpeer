package ui

import (
	"fmt"
	"strings"
)

func chatView(m Model, width, height int) string {
	if m.activePeer == nil {
		empty := StyleHelp.Render("Select a peer to start chatting")
		return StylePanel.Width(width).Height(height).Render(empty)
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
	if m.fileXfer != nil && !m.fileXfer.Done {
		pct := 0
		if m.fileXfer.Total > 0 {
			pct = int(float64(m.fileXfer.Received) / float64(m.fileXfer.Total) * 100)
		}
		xfer = StyleHelp.Render(fmt.Sprintf("Receiving %s... %d%%", m.fileXfer.Name, pct)) + "\n"
	} else if m.fileXfer != nil && m.fileXfer.Done {
		xfer = StyleHelp.Render(fmt.Sprintf("✓ %s saved to %s", m.fileXfer.Name, m.fileXfer.SavePath)) + "\n"
	}

	help := StyleHelp.Render("Enter send  /file <path> send file  Esc quit")
	body := header + "\n" + msgArea + "\n" + xfer + input + "\n" + help
	return StylePanel.Width(width).Height(height).Render(body)
}
