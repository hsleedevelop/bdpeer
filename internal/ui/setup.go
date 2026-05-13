package ui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type MsgNicknameSet struct{ Nickname string }

func setupView(m Model) string {
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorPrimary).
		Padding(1, 3).
		Width(40)

	content := strings.Join([]string{
		StyleTitle.Render("bdpeer"),
		"",
		StyleHelp.Render("P2P file & message sharing"),
		"",
		"Enter your nickname:",
		"",
		"> " + m.inputBuf + "█",
		"",
		StyleHelp.Render("Press Enter to confirm"),
	}, "\n")

	return lipgloss.Place(m.width, m.height,
		lipgloss.Center, lipgloss.Center,
		box.Render(content),
	)
}

func (m Model) handleSetupKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEnter:
		nick := strings.TrimSpace(m.inputBuf)
		if nick == "" {
			return m, nil
		}
		m.nickname = nick
		m.inputBuf = ""
		m.screen = ScreenMain
		if m.nickCh != nil {
			ch := m.nickCh
			return m, func() tea.Msg {
				go func() { ch <- nick }()
				return nil
			}
		}
		return m, nil
	case tea.KeyBackspace, tea.KeyDelete:
		if len(m.inputBuf) > 0 {
			m.inputBuf = m.inputBuf[:len(m.inputBuf)-1]
		}
	case tea.KeyCtrlC, tea.KeyEsc:
		return m, tea.Quit
	default:
		if msg.Type == tea.KeyRunes {
			m.inputBuf += string(msg.Runes)
		}
	}
	return m, nil
}
