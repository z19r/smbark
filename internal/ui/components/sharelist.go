package components

import (
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/z19r/smbark/internal/smb"
	"github.com/z19r/smbark/internal/theme"
)

type ShareItem struct {
	Share smb.Share
}

func (s ShareItem) Title() string {
	icon := "📁"
	switch s.Share.Type {
	case smb.ShareTypePrinter:
		icon = "🖨"
	case smb.ShareTypeIPC:
		icon = "🔗"
	}
	return fmt.Sprintf("%s %s", icon, s.Share.Name)
}

func (s ShareItem) Description() string {
	t := theme.Active
	var parts []string
	parts = append(parts, s.Share.Path)
	if s.Share.Comment != "" {
		parts = append(parts, s.Share.Comment)
	}
	if s.Share.IsMounted {
		mounted := lipgloss.NewStyle().Foreground(t.Green).Render("● mounted")
		parts = append(parts, mounted)
		if s.Share.MountPoint != "" {
			parts = append(parts, "→ "+s.Share.MountPoint)
		}
	}
	return strings.Join(parts, "  ")
}

func (s ShareItem) FilterValue() string {
	return s.Share.Name + " " + s.Share.Host + " " + s.Share.Comment
}

type ShareListDelegate struct {
	theme *theme.Theme
}

func NewShareListDelegate(t *theme.Theme) ShareListDelegate {
	return ShareListDelegate{theme: t}
}

func (d ShareListDelegate) Height() int                             { return 3 }
func (d ShareListDelegate) Spacing() int                            { return 1 }
func (d ShareListDelegate) Update(_ tea.Msg, _ *list.Model) tea.Cmd { return nil }

func (d ShareListDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	si, ok := item.(ShareItem)
	if !ok {
		return
	}

	t := d.theme
	isSelected := index == m.Index()

	var statusIcon string
	var statusColor lipgloss.Color
	switch {
	case si.Share.IsMounted && si.Share.IsAutomount:
		statusIcon = "⟐"
		statusColor = t.Cyan
	case si.Share.IsMounted:
		statusIcon = "◉"
		statusColor = t.Green
	case si.Share.IsAutomount:
		statusIcon = "◎"
		statusColor = t.Yellow
	default:
		statusIcon = "○"
		statusColor = t.Muted
	}

	titleStyle := lipgloss.NewStyle().Foreground(t.Foreground)
	descStyle := lipgloss.NewStyle().Foreground(t.DarkForeground)
	borderStyle := lipgloss.NewStyle().Foreground(t.Muted)

	if isSelected {
		titleStyle = titleStyle.Foreground(t.Accent).Bold(true)
		descStyle = descStyle.Foreground(t.LightForeground)
		borderStyle = borderStyle.Foreground(t.Accent)
	}

	icon := "📁"
	switch si.Share.Type {
	case smb.ShareTypePrinter:
		icon = "🖨️"
	case smb.ShareTypeIPC:
		icon = "🔗"
	}

	title := titleStyle.Render(fmt.Sprintf("%s %s  %s",
		icon, si.Share.Name,
		lipgloss.NewStyle().Foreground(statusColor).Render(statusIcon)))

	desc := descStyle.Render(si.Share.Path)
	if si.Share.Comment != "" {
		desc += descStyle.Render(" — " + si.Share.Comment)
	}

	var usageBar string
	if si.Share.IsMounted && si.Share.SizeTotal > 0 {
		pct := float64(si.Share.SizeUsed) / float64(si.Share.SizeTotal)
		barWidth := 20
		filled := int(pct * float64(barWidth))
		if filled > barWidth {
			filled = barWidth
		}

		barColor := t.Green
		if pct > 0.8 {
			barColor = t.Red
		} else if pct > 0.6 {
			barColor = t.Yellow
		}

		bar := lipgloss.NewStyle().Foreground(barColor).Render(strings.Repeat("█", filled)) +
			lipgloss.NewStyle().Foreground(t.Muted).Render(strings.Repeat("░", barWidth-filled))

		usageBar = fmt.Sprintf("  %s %s/%s (%.0f%%)",
			bar,
			smb.FormatSize(si.Share.SizeUsed),
			smb.FormatSize(si.Share.SizeTotal),
			pct*100)
	}

	prefix := "  "
	if isSelected {
		prefix = borderStyle.Render("▎ ")
	}

	content := prefix + title + "\n" + prefix + desc
	if usageBar != "" {
		content += "\n" + prefix + lipgloss.NewStyle().Foreground(t.DarkForeground).Render(usageBar)
	}

	fmt.Fprint(w, content)
}

func ShareListKeyMap() list.KeyMap {
	return list.KeyMap{
		CursorUp:             key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/k", "up")),
		CursorDown:           key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓/j", "down")),
		PrevPage:             key.NewBinding(key.WithKeys("pgup", "b"), key.WithHelp("pgup", "prev page")),
		NextPage:             key.NewBinding(key.WithKeys("pgdown", "f"), key.WithHelp("pgdn", "next page")),
		GoToStart:            key.NewBinding(key.WithKeys("home", "g"), key.WithHelp("home", "start")),
		GoToEnd:              key.NewBinding(key.WithKeys("end", "G"), key.WithHelp("end", "end")),
		Filter:               key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "filter")),
		ClearFilter:          key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "clear filter")),
		CancelWhileFiltering: key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "cancel")),
		AcceptWhileFiltering: key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "apply filter")),
		ShowFullHelp:         key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
		CloseFullHelp:        key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "close help")),
	}
}
