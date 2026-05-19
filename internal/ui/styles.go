package ui

import "github.com/charmbracelet/lipgloss"

// panelStyle returns the bordered panel style with the active highlight when
// focused matches the current focus area.
func panelStyle(active bool) lipgloss.Style {
	if active {
		return StylePanelActive
	}
	return StylePanel
}

var (
	colorBase    = lipgloss.Color("#2E3440")
	colorSurface = lipgloss.Color("#3B4252")
	colorPrimary = lipgloss.Color("#88C0D0")
	colorSecond  = lipgloss.Color("#81A1C1")
	colorGreen   = lipgloss.Color("#A3BE8C")
	colorRed     = lipgloss.Color("#BF616A")
	colorText    = lipgloss.Color("#ECEFF4")
	colorMuted   = lipgloss.Color("#4C566A")

	StyleTitle = lipgloss.NewStyle().
			Foreground(colorPrimary).
			Bold(true).
			Padding(0, 1)

	StylePanel = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorMuted)

	StylePanelActive = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(colorPrimary)

	StylePeerOnline = lipgloss.NewStyle().
			Foreground(colorGreen)

	StylePeerOffline = lipgloss.NewStyle().
			Foreground(colorMuted)

	StyleMessage = lipgloss.NewStyle().
			Foreground(colorText)

	StyleMessageMine = lipgloss.NewStyle().
			Foreground(colorSecond)

	StyleInput = lipgloss.NewStyle().
			Foreground(colorText).
			BorderStyle(lipgloss.NormalBorder()).
			BorderTop(true).
			BorderForeground(colorMuted)

	StyleHelp = lipgloss.NewStyle().
			Foreground(colorMuted).
			Italic(true)

	StyleError = lipgloss.NewStyle().
			Foreground(colorRed)
)
