package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// truncateForWidth shrinks s with an ellipsis when its rune count exceeds max.
// Approximates display width using rune count — adequate for the file panel.
func truncateForWidth(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	if max <= 1 {
		return "…"
	}
	runes := []rune(s)
	return "…" + string(runes[len(runes)-(max-1):])
}

type fileEntry struct {
	Name  string
	IsDir bool
}

func focusBar(f focusArea) string {
	on := StyleMessageMine.Render
	off := StyleHelp.Render
	p, c, fi := off("[1.피어]"), off("[2.채팅]"), off("[3.파일]")
	switch f {
	case focusPeers:
		p = on("[1.피어]")
	case focusChat:
		c = on("[2.채팅]")
	case focusFiles:
		fi = on("[3.파일]")
	}
	return p + " " + c + " " + fi
}

func readDir(path string) []fileEntry {
	var out []fileEntry
	if parent := filepath.Dir(path); parent != path {
		out = append(out, fileEntry{Name: "..", IsDir: true})
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return out
	}
	var dirs, files []fileEntry
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if e.IsDir() {
			dirs = append(dirs, fileEntry{Name: e.Name(), IsDir: true})
		} else {
			files = append(files, fileEntry{Name: e.Name(), IsDir: false})
		}
	}
	out = append(out, dirs...)
	out = append(out, files...)
	return out
}

func fileTreeView(m Model, width, height int) string {
	header := StyleTitle.Render("Files")

	if m.confirmPath != "" {
		return confirmDialog(m, width, height)
	}

	cwd := truncateForWidth(m.fileCwd, width-4)
	pathLine := StyleHelp.Render(cwd)

	// Reserve rows for: border(2) + header(1) + pathLine(1) + helpLine(1) + scrollLine(1)
	listHeight := height - 6
	if listHeight < 1 {
		listHeight = 1
	}

	// Clamp fileIdx so renders stay within entries even after dir changes.
	if m.fileIdx >= len(m.fileEntries) {
		m.fileIdx = len(m.fileEntries) - 1
	}
	if m.fileIdx < 0 {
		m.fileIdx = 0
	}

	start := 0
	if m.fileIdx >= listHeight {
		start = m.fileIdx - listHeight + 1
	}
	end := start + listHeight
	if end > len(m.fileEntries) {
		end = len(m.fileEntries)
	}

	// Filenames may exceed width — truncate so lipgloss does not wrap and
	// extend the panel vertically. Two chars are reserved for the "▸ " prefix.
	nameMax := width - 4
	if nameMax < 4 {
		nameMax = 4
	}

	var lines []string
	for i := start; i < end; i++ {
		e := m.fileEntries[i]
		name := e.Name
		if e.IsDir {
			name = name + "/"
		}
		name = truncateForWidth(name, nameMax)
		if i == m.fileIdx && m.focus == focusFiles {
			lines = append(lines, StyleMessageMine.Render("▸ "+name))
		} else {
			lines = append(lines, StyleMessage.Render("  "+name))
		}
	}
	// Pad to listHeight so the panel keeps a stable size on short dirs.
	for len(lines) < listHeight {
		lines = append(lines, "")
	}

	scroll := ""
	if len(m.fileEntries) > 0 {
		scroll = fmt.Sprintf("%d/%d", m.fileIdx+1, len(m.fileEntries))
		if end < len(m.fileEntries) {
			scroll += " ↓"
		}
		if start > 0 {
			scroll = "↑ " + scroll
		}
	}
	scrollLine := StyleHelp.Render(scroll)

	help := "→ 포커스  ↑/↓ 이동  Enter 선택"
	if m.focus == focusFiles {
		help = "← 채팅으로  ↑/↓ 이동  Enter 진입/전송"
	}
	helpLine := StyleHelp.Render(help)

	body := header + "\n" + pathLine + "\n" + strings.Join(lines, "\n") + "\n" + scrollLine + "\n" + helpLine
	return panelStyle(m.focus == focusFiles).Width(width).Height(height).Render(body)
}

// confirmDialog renders the file-send confirmation prompt. Because it is
// triggered from the file panel, treat it as the focused area.
func confirmDialog(m Model, width, height int) string {
	header := StyleTitle.Render("파일 전송")
	target := "(피어 미선택)"
	if m.activePeer != nil {
		target = m.activePeer.Nickname
	}
	name := filepath.Base(m.confirmPath)
	body := header + "\n\n" +
		StyleMessage.Render("To: ") + target + "\n" +
		StyleMessage.Render("File: ") + name + "\n\n" +
		StyleHelp.Render("전송하시겠습니까?") + "\n" +
		StyleMessageMine.Render("  [Y] 예    [N] 아니오")
	return StylePanelActive.Width(width).Height(height).Render(body)
}
