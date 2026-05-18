package ui

import (
	"os"
	"path/filepath"
	"strings"
)

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

	cwd := m.fileCwd
	if len(cwd) > width-4 {
		cwd = "..." + cwd[len(cwd)-(width-7):]
	}
	pathLine := StyleHelp.Render(cwd)

	listHeight := height - 5
	if listHeight < 1 {
		listHeight = 1
	}

	start := 0
	if m.fileIdx >= listHeight {
		start = m.fileIdx - listHeight + 1
	}
	end := start + listHeight
	if end > len(m.fileEntries) {
		end = len(m.fileEntries)
	}

	var lines []string
	for i := start; i < end; i++ {
		e := m.fileEntries[i]
		name := e.Name
		if e.IsDir {
			name = name + "/"
		}
		if i == m.fileIdx && m.focus == focusFiles {
			lines = append(lines, StyleMessageMine.Render("▸ "+name))
		} else {
			lines = append(lines, StyleMessage.Render("  "+name))
		}
	}

	help := "→ 포커스  ↑/↓ 이동  Enter 선택"
	if m.focus == focusFiles {
		help = "← 피어로  ↑/↓ 이동  Enter 진입/전송"
	}
	helpLine := StyleHelp.Render(help)

	body := header + "\n" + pathLine + "\n" + strings.Join(lines, "\n") + "\n" + helpLine
	return StylePanel.Width(width).Height(height).Render(body)
}

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
	return StylePanel.Width(width).Height(height).Render(body)
}
