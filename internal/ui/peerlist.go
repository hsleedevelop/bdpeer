package ui

import (
	"fmt"
	"strings"
)

func peerListView(m Model, width, height int) string {
	var sb strings.Builder
	sb.WriteString(StyleTitle.Render("Peers") + "\n")
	if m.nickname != "" {
		sb.WriteString(StyleHelp.Render("나: "+m.nickname) + "\n")
	}
	sb.WriteString("\n")

	if len(m.peers) == 0 {
		sb.WriteString(StyleHelp.Render("searching...") + "\n")
	}
	for _, p := range m.peers {
		name := p.Nickname
		if name == "" && p.ID != "" {
			s := p.ID.String()
			if len(s) > 8 {
				name = s[:8]
			} else {
				name = s
			}
		} else if name == "" {
			name = p.Source
		}
		cursor := "  "
		if m.activePeer != nil && m.activePeer.ID == p.ID {
			cursor = "▶ "
		}
		line := fmt.Sprintf("%s%s", cursor, StylePeerOnline.Render(name))
		sb.WriteString(line + "\n")
	}

	sb.WriteString("\n" + StyleHelp.Render("↑/↓ select  q quit"))
	return StylePanel.Width(width).Height(height).Render(sb.String())
}
