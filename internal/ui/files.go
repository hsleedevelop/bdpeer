package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	runewidth "github.com/mattn/go-runewidth"
)

// truncateForWidth shrinks s with an ellipsis when its display width (terminal
// cells) exceeds max. Korean/CJK runes occupy 2 cells each, so rune counting
// would under-estimate and let names wrap, which breaks the panel layout.
func truncateForWidth(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if runewidth.StringWidth(s) <= max {
		return s
	}
	if max <= 1 {
		return "…"
	}
	// Keep the tail; ellipsis is 1 cell wide.
	budget := max - 1
	runes := []rune(s)
	width := 0
	cut := len(runes)
	for i := len(runes) - 1; i >= 0; i-- {
		w := runewidth.RuneWidth(runes[i])
		if width+w > budget {
			break
		}
		width += w
		cut = i
	}
	return "…" + string(runes[cut:])
}

type fileEntry struct {
	Name  string
	IsDir bool
}

func focusBar(f focusArea) string {
	on := StyleMessageMine.Render
	off := StyleHelp.Render
	p, c, fi := off("[1.Peers]"), off("[2.Chat]"), off("[3.Files]")
	switch f {
	case focusPeers:
		p = on("[1.Peers]")
	case focusChat:
		c = on("[2.Chat]")
	case focusFiles:
		fi = on("[3.Files]")
	}
	return p + " " + c + " " + fi
}

// visibleFileEntries returns entries matching the case-insensitive substring
// filter. ".." is always kept so users can navigate out even while filtering.
func visibleFileEntries(entries []fileEntry, filter string) []fileEntry {
	if filter == "" {
		return entries
	}
	q := strings.ToLower(filter)
	out := make([]fileEntry, 0, len(entries))
	for _, e := range entries {
		if e.Name == ".." {
			out = append(out, e)
			continue
		}
		if strings.Contains(strings.ToLower(e.Name), q) {
			out = append(out, e)
		}
	}
	return out
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

	entries := visibleFileEntries(m.fileEntries, m.fileFilter)

	// Reserve rows for: border(2) + header(1) + pathLine(1) + filterLine(1) + helpLine(1) + scrollLine(1)
	listHeight := height - 7
	if listHeight < 1 {
		listHeight = 1
	}

	// Clamp fileIdx so renders stay within entries even after dir/filter changes.
	if m.fileIdx >= len(entries) {
		m.fileIdx = len(entries) - 1
	}
	if m.fileIdx < 0 {
		m.fileIdx = 0
	}

	start := 0
	if m.fileIdx >= listHeight {
		start = m.fileIdx - listHeight + 1
	}
	end := start + listHeight
	if end > len(entries) {
		end = len(entries)
	}

	// Filenames may exceed width — truncate so lipgloss does not wrap and
	// extend the panel vertically. Two chars are reserved for the "▸ " prefix.
	nameMax := width - 4
	if nameMax < 4 {
		nameMax = 4
	}

	var lines []string
	for i := start; i < end; i++ {
		e := entries[i]
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
	if len(entries) > 0 {
		scroll = fmt.Sprintf("%d/%d", m.fileIdx+1, len(entries))
		if end < len(entries) {
			scroll += " ↓"
		}
		if start > 0 {
			scroll = "↑ " + scroll
		}
	}
	if m.fileFilter != "" && len(entries) != len(m.fileEntries) {
		scroll = fmt.Sprintf("%s (전체 %d)", scroll, len(m.fileEntries))
	}
	scrollLine := StyleHelp.Render(truncateForWidth(scroll, width-2))

	filterText := ""
	switch {
	case m.fileFiltering:
		filterText = "Filter: " + m.fileFilter + "▏"
	case m.fileFilter != "":
		filterText = "Filter: " + m.fileFilter
	}
	var filterLine string
	if filterText != "" {
		filterLine = StyleMessageMine.Render(truncateForWidth(filterText, width-2))
	} else {
		filterLine = ""
	}

	help := "→포커스 ↑↓이동 Enter선택"
	switch {
	case m.fileFiltering:
		help = "Type filter  Enter apply  Esc cancel"
	case m.focus == focusFiles:
		help = "←Chat ↑↓ move  Enter select  /filter"
		if m.fileFilter != "" {
			help = "←Chat ↑↓ move  Enter select  /edit  Esc clear"
		}
	}
	helpLine := StyleHelp.Render(truncateForWidth(help, width-2))

	body := header + "\n" + pathLine + "\n" + filterLine + "\n" + strings.Join(lines, "\n") + "\n" + scrollLine + "\n" + helpLine
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
