package ui

import "github.com/charmbracelet/lipgloss"

// Catppuccin Mocha, the palette Superfile uses by default.
var (
	cText     = lipgloss.Color("#cdd6f4")
	cSubtext  = lipgloss.Color("#a6adc8")
	cOverlay  = lipgloss.Color("#6c7086")
	cSurface  = lipgloss.Color("#45475a")
	cBase     = lipgloss.Color("#1e1e2e")
	cLavender = lipgloss.Color("#b4befe")
	cBlue     = lipgloss.Color("#89b4fa")
	cGreen    = lipgloss.Color("#a6e3a1")
	cYellow   = lipgloss.Color("#f9e2af")
	cRed      = lipgloss.Color("#f38ba8")
	cMauve    = lipgloss.Color("#cba6f7")
	cPeach    = lipgloss.Color("#fab387")
)

var (
	borderStyle      = lipgloss.NewStyle().Foreground(cOverlay)
	borderFocusStyle = lipgloss.NewStyle().Foreground(cLavender)
	titleStyle       = lipgloss.NewStyle().Foreground(cBase).Background(cLavender).Bold(true).Padding(0, 1)
	titleDimStyle    = lipgloss.NewStyle().Foreground(cText).Background(cSurface).Padding(0, 1)
	textStyle        = lipgloss.NewStyle().Foreground(cText)
	dimStyle         = lipgloss.NewStyle().Foreground(cOverlay)
	subStyle         = lipgloss.NewStyle().Foreground(cSubtext)
	headerStyle      = lipgloss.NewStyle().Foreground(cMauve).Bold(true)
	selectedStyle    = lipgloss.NewStyle().Foreground(cText).Background(cSurface).Bold(true)
	bookTitleStyle   = lipgloss.NewStyle().Foreground(cText).Bold(true)
	authorStyle      = lipgloss.NewStyle().Foreground(cBlue)
	labelStyle       = lipgloss.NewStyle().Foreground(cOverlay)
	keyStyle         = lipgloss.NewStyle().Foreground(cLavender).Bold(true)
	errStyle         = lipgloss.NewStyle().Foreground(cRed)
	okStyle          = lipgloss.NewStyle().Foreground(cGreen)
	badgeOnStyle     = lipgloss.NewStyle().Foreground(cBase).Background(cGreen).Bold(true).Padding(0, 1)
	badgeOffStyle    = lipgloss.NewStyle().Foreground(cText).Background(cSurface).Padding(0, 1)
	badgeSyncStyle   = lipgloss.NewStyle().Foreground(cBase).Background(cPeach).Bold(true).Padding(0, 1)
	badgeAskStyle    = lipgloss.NewStyle().Foreground(cBase).Background(cYellow).Bold(true).Padding(0, 1)
)
