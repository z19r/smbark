package ui

import (
	"math"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/z19r/smbark/internal/theme"
)

type Tab struct {
	Name string
	Icon string
}

var Tabs = []Tab{
	{Name: "Discover", Icon: "🔍"},
	{Name: "Mounted", Icon: "💾"},
	{Name: "Automount", Icon: "⚡"},
	{Name: "Config", Icon: "⚙"},
}

func RenderTabs(activeIdx int, width int, frame float64) string {
	t := theme.Active

	var renderedTabs []string
	tabWidth := max((width-4)/len(Tabs), 12)

	for i, tab := range Tabs {
		label := tab.Icon + " " + tab.Name

		if i == activeIdx {
			offset := math.Mod(frame*0.03+float64(i)*0.2, 1.0)
			c := t.RainbowColor(offset)
			style := lipgloss.NewStyle().
				Foreground(t.DarkerBackground).
				Background(lipgloss.Color(c)).
				Bold(true).
				Padding(0, 1).
				Width(tabWidth).
				Align(lipgloss.Center)
			renderedTabs = append(renderedTabs, style.Render(label))
		} else {
			style := lipgloss.NewStyle().
				Foreground(t.DarkForeground).
				Background(t.DarkBackground).
				Padding(0, 1).
				Width(tabWidth).
				Align(lipgloss.Center)
			renderedTabs = append(renderedTabs, style.Render(label))
		}
	}

	tabBar := lipgloss.JoinHorizontal(lipgloss.Top, renderedTabs...)

	underline := renderTabUnderline(activeIdx, len(Tabs), tabWidth, width, t, frame)

	return lipgloss.JoinVertical(lipgloss.Left, tabBar, underline)
}

func renderTabUnderline(activeIdx, totalTabs, tabWidth, totalWidth int, t *theme.Theme, frame float64) string {
	var sb strings.Builder
	for i := range totalWidth {
		tabIdx := i / max(tabWidth, 1)
		if tabIdx >= totalTabs {
			tabIdx = totalTabs - 1
		}
		if tabIdx == activeIdx {
			pos := math.Mod(float64(i)/float64(max(totalWidth, 1))+frame*0.02, 1.0)
			c := t.RainbowColor(pos)
			sb.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(c)).Render("▀"))
		} else {
			sb.WriteString(lipgloss.NewStyle().Foreground(t.Selection).Render("─"))
		}
	}
	return sb.String()
}
