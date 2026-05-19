package ui

import (
	"fmt"
	"strings"
)

func sourceBadge(source string) string {
	switch source {
	case "mdns", "bonjour":
		return "[로컬]"
	case "ssdp", "wsd":
		return "[로컬]"
	case "ble":
		return "[BLE]"
	case "ble→webrtc":
		return "[BLE▸WTC]"
	case "dht":
		return "[DHT]"
	default:
		if source != "" {
			return "[" + source + "]"
		}
		return ""
	}
}

func peerListView(m Model, width, height int) string {
	var sb strings.Builder
	sb.WriteString(StyleTitle.Render("Peers") + "\n")
	if m.nickname != "" {
		sb.WriteString(StyleHelp.Render("나: "+m.nickname) + "\n")
	}
	sb.WriteString("\n")

	visible := 0
	for _, p := range m.peers {
		if p.Nickname == "" {
			continue // 닉네임 교환 전 피어는 표시하지 않음
		}
		visible++
		cursor := "  "
		if m.activePeer != nil && m.activePeer.ID == p.ID {
			cursor = "▶ "
		}
		badge := sourceBadge(p.Source)
		line := fmt.Sprintf("%s%s %s", cursor, StylePeerOnline.Render(p.Nickname), StyleHelp.Render(badge))
		sb.WriteString(line + "\n")
	}
	if visible == 0 {
		sb.WriteString(StyleHelp.Render("searching...") + "\n")
	}

	sb.WriteString("\n" + StyleHelp.Render("↑/↓ select  → 채팅으로  q quit"))
	return panelStyle(m.focus == focusPeers).Width(width).Height(height).Render(sb.String())
}
