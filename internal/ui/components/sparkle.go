package components

import (
	"math"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/z19r/smbark/internal/theme"
)

var sparkleChars = []string{"✦", "✧", "⊹", "˚", "⋆", "·", "✶", "✴", "✸", "★"}

func AnimatedDivider(width int, frame float64) string {
	t := theme.Active
	var sb strings.Builder

	for i := range width {
		pos := math.Mod(float64(i)/float64(max(width-1, 1))+frame*0.015, 1.0)
		c := t.RainbowColor(pos)

		charIdx := int(math.Mod(float64(i)+frame*0.5, float64(len(sparkleChars))))
		if i%3 == 0 {
			sb.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(c)).Render(sparkleChars[charIdx]))
		} else {
			sb.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(c)).Render("─"))
		}
	}
	return sb.String()
}

func PulsingDot(frame float64, baseColor lipgloss.Color) string {
	intensity := (math.Sin(frame*0.1) + 1) / 2
	if intensity > 0.5 {
		return lipgloss.NewStyle().Foreground(baseColor).Bold(true).Render("●")
	}
	return lipgloss.NewStyle().Foreground(baseColor).Render("○")
}

func WaveText(text string, frame float64, t *theme.Theme) string {
	runes := []rune(text)
	var sb strings.Builder
	for i, r := range runes {
		if r == ' ' {
			sb.WriteRune(r)
			continue
		}
		wave := math.Sin(float64(i)*0.5 + frame*0.15)
		pos := math.Mod((wave+1)/2, 1.0)
		c := t.RainbowColor(pos)
		sb.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(c)).Render(string(r)))
	}
	return sb.String()
}

func NetworkAnimation(frame float64, width int) string {
	t := theme.Active
	nodes := []string{"🖥", "📡", "🗄", "💻", "🖨"}
	var sb strings.Builder

	spacing := max(width/(len(nodes)+1), 8)
	connChar := "═"

	for i, node := range nodes {
		if i > 0 {
			connWidth := spacing - 3
			for j := range connWidth {
				pos := math.Mod(float64(j)/float64(max(connWidth-1, 1))+frame*0.03+float64(i)*0.1, 1.0)
				pulse := (math.Sin(float64(j)*0.3+frame*0.2) + 1) / 2
				if pulse > 0.6 {
					c := t.RainbowColor(pos)
					sb.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(c)).Bold(true).Render(connChar))
				} else {
					sb.WriteString(lipgloss.NewStyle().Foreground(t.Selection).Render("─"))
				}
			}
		}
		sb.WriteString(" " + node + " ")
	}
	return sb.String()
}

func MiniProgressBar(percent float64, width int, t *theme.Theme) string {
	filled := int(percent * float64(width))
	if filled > width {
		filled = width
	}
	if filled < 0 {
		filled = 0
	}

	var sb strings.Builder
	sb.WriteString(lipgloss.NewStyle().Foreground(t.Muted).Render("╢"))
	for i := range width {
		pos := float64(i) / float64(max(width-1, 1))
		if i < filled {
			c := t.RainbowColor(pos)
			sb.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(c)).Render("█"))
		} else {
			sb.WriteString(lipgloss.NewStyle().Foreground(t.Selection).Render("░"))
		}
	}
	sb.WriteString(lipgloss.NewStyle().Foreground(t.Muted).Render("╟"))
	return sb.String()
}

func BoxWithGlow(content string, width int, frame float64, t *theme.Theme) string {
	lines := strings.Split(content, "\n")
	maxLen := 0
	for _, l := range lines {
		if lipgloss.Width(l) > maxLen {
			maxLen = lipgloss.Width(l)
		}
	}

	boxWidth := min(max(maxLen+4, 20), width)

	topLeft := "╭"
	topRight := "╮"
	bottomLeft := "╰"
	bottomRight := "╯"
	horizontal := "─"
	vertical := "│"

	var sb strings.Builder

	topPos := math.Mod(frame*0.02, 1.0)
	topC := t.RainbowColor(topPos)
	sb.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(topC)).Render(topLeft))
	for i := range boxWidth - 2 {
		pos := math.Mod(float64(i)/float64(max(boxWidth-3, 1))+frame*0.02, 1.0)
		c := t.RainbowColor(pos)
		sb.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(c)).Render(horizontal))
	}
	sb.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(topC)).Render(topRight))
	sb.WriteString("\n")

	for _, line := range lines {
		leftPos := math.Mod(frame*0.02+0.1, 1.0)
		lc := t.RainbowColor(leftPos)
		rightPos := math.Mod(frame*0.02+0.9, 1.0)
		rc := t.RainbowColor(rightPos)

		padding := max(boxWidth-2-lipgloss.Width(line), 0)
		sb.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(lc)).Render(vertical))
		sb.WriteString(" " + line + strings.Repeat(" ", padding-1))
		sb.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(rc)).Render(vertical))
		sb.WriteString("\n")
	}

	botPos := math.Mod(frame*0.02+0.5, 1.0)
	botC := t.RainbowColor(botPos)
	sb.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(botC)).Render(bottomLeft))
	for i := range boxWidth - 2 {
		pos := math.Mod(float64(i)/float64(max(boxWidth-3, 1))+frame*0.02+0.5, 1.0)
		c := t.RainbowColor(pos)
		sb.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(c)).Render(horizontal))
	}
	sb.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(botC)).Render(bottomRight))

	return sb.String()
}
