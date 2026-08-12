package components

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/z19r/smbark/internal/theme"
)

const logo = `
  ███████╗███╗   ███╗██████╗  █████╗ ██████╗ ██╗  ██╗
  ██╔════╝████╗ ████║██╔══██╗██╔══██╗██╔══██╗██║ ██╔╝
  ███████╗██╔████╔██║██████╔╝███████║██████╔╝█████╔╝
  ╚════██║██║╚██╔╝██║██╔══██╗██╔══██║██╔══██╗██╔═██╗
  ███████║██║ ╚═╝ ██║██████╔╝██║  ██║██║  ██║██║  ██╗
  ╚══════╝╚═╝     ╚═╝╚═════╝ ╚═╝  ╚═╝╚═╝  ╚═╝╚═╝  ╚═╝`

func RenderHeader(width int, frame float64) string {
	t := theme.Active

	lines := strings.Split(strings.TrimPrefix(logo, "\n"), "\n")
	var coloredLines []string
	for i, line := range lines {
		offset := frame*0.02 + float64(i)*0.05
		coloredLines = append(coloredLines, t.RainbowWithOffset(line, offset))
	}
	coloredLogo := strings.Join(coloredLines, "\n")

	subtitle := t.Gradient("✦ SMB Share Manager ✦", t.Cyan, t.Magenta)

	bar := theme.GradientBar(min(width-4, 60), t)

	header := lipgloss.JoinVertical(lipgloss.Center,
		coloredLogo,
		"",
		subtitle,
		bar,
	)

	return lipgloss.NewStyle().
		Width(width).
		Align(lipgloss.Center).
		Render(header)
}

func RenderStatusBar(width int, activeTab string, mountedCount int, t *theme.Theme) string {
	left := lipgloss.NewStyle().
		Foreground(t.DarkerBackground).
		Background(t.Accent).
		Bold(true).
		Padding(0, 1).
		Render(fmt.Sprintf(" ⊹ %s ", activeTab))

	mountBadge := lipgloss.NewStyle().
		Foreground(t.DarkerBackground).
		Background(t.Green).
		Padding(0, 1).
		Render(fmt.Sprintf("◉ %d mounted", mountedCount))

	right := lipgloss.NewStyle().
		Foreground(t.DarkForeground).
		Padding(0, 1).
		Render("q:quit  ?:help  tab:switch")

	gap := max(width-lipgloss.Width(left)-lipgloss.Width(mountBadge)-lipgloss.Width(right), 0)

	bar := lipgloss.NewStyle().
		Background(t.DarkBackground).
		Width(width).
		Render(left + " " + mountBadge + strings.Repeat(" ", gap) + right)

	return bar
}

func RenderBreadcrumb(items []string, t *theme.Theme) string {
	var parts []string
	for i, item := range items {
		style := lipgloss.NewStyle().Foreground(t.DarkForeground)
		if i == len(items)-1 {
			style = lipgloss.NewStyle().Foreground(t.Accent).Bold(true)
		}
		parts = append(parts, style.Render(item))
	}
	sep := lipgloss.NewStyle().Foreground(t.Muted).Render(" › ")
	return strings.Join(parts, sep)
}
