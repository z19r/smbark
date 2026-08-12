package components

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/z19r/smbark/internal/theme"
)

type DialogField struct {
	Label       string
	Placeholder string
	Value       string
	Password    bool
}

type DialogModel struct {
	Title    string
	Fields   []DialogField
	Inputs   []textinput.Model
	FocusIdx int
	Width    int
	Done     bool
	Canceled bool
}

type DialogResult struct {
	Values   map[string]string
	Canceled bool
}

func NewDialog(title string, fields []DialogField, width int) DialogModel {
	t := theme.Active
	var inputs []textinput.Model
	for _, f := range fields {
		ti := textinput.New()
		ti.Placeholder = f.Placeholder
		ti.SetValue(f.Value)
		ti.CharLimit = 256
		ti.Width = width - 10
		ti.Cursor.Style = lipgloss.NewStyle().Foreground(t.Accent)
		ti.PromptStyle = lipgloss.NewStyle().Foreground(t.Accent)
		ti.TextStyle = lipgloss.NewStyle().Foreground(t.Foreground)
		ti.PlaceholderStyle = lipgloss.NewStyle().Foreground(t.Muted)
		if f.Password {
			ti.EchoMode = textinput.EchoPassword
			ti.EchoCharacter = '●'
		}
		inputs = append(inputs, ti)
	}
	if len(inputs) > 0 {
		inputs[0].Focus()
	}

	return DialogModel{
		Title:  title,
		Fields: fields,
		Inputs: inputs,
		Width:  width,
	}
}

func (d DialogModel) Update(msg tea.Msg) (DialogModel, tea.Cmd) {
	if keyMsg, ok := msg.(tea.KeyMsg); ok {
		switch keyMsg.String() {
		case "tab", "down":
			d.FocusIdx = (d.FocusIdx + 1) % len(d.Inputs)
			for i := range d.Inputs {
				if i == d.FocusIdx {
					d.Inputs[i].Focus()
				} else {
					d.Inputs[i].Blur()
				}
			}
			return d, nil
		case "shift+tab", "up":
			d.FocusIdx = (d.FocusIdx - 1 + len(d.Inputs)) % len(d.Inputs)
			for i := range d.Inputs {
				if i == d.FocusIdx {
					d.Inputs[i].Focus()
				} else {
					d.Inputs[i].Blur()
				}
			}
			return d, nil
		case "enter":
			if d.FocusIdx == len(d.Inputs)-1 {
				d.Done = true
				return d, nil
			}
			d.FocusIdx++
			for i := range d.Inputs {
				if i == d.FocusIdx {
					d.Inputs[i].Focus()
				} else {
					d.Inputs[i].Blur()
				}
			}
			return d, nil
		case "esc":
			d.Canceled = true
			d.Done = true
			return d, nil
		}
	}

	var cmd tea.Cmd
	d.Inputs[d.FocusIdx], cmd = d.Inputs[d.FocusIdx].Update(msg)
	return d, cmd
}

func (d DialogModel) Values() map[string]string {
	vals := make(map[string]string)
	for i, f := range d.Fields {
		vals[f.Label] = d.Inputs[i].Value()
	}
	return vals
}

func (d DialogModel) View() string {
	t := theme.Active

	titleStyle := lipgloss.NewStyle().
		Foreground(t.Accent).
		Bold(true).
		Padding(0, 1)

	labelStyle := lipgloss.NewStyle().
		Foreground(t.LightForeground).
		Width(14).
		Align(lipgloss.Right).
		MarginRight(1)

	var rows []string
	for i, f := range d.Fields {
		indicator := "  "
		if i == d.FocusIdx {
			indicator = lipgloss.NewStyle().Foreground(t.Accent).Render("▸ ")
		}
		label := labelStyle.Render(f.Label + ":")
		input := d.Inputs[i].View()
		rows = append(rows, indicator+label+input)
	}

	hint := lipgloss.NewStyle().
		Foreground(t.DarkForeground).
		Italic(true).
		Render("  tab:next  enter:submit  esc:cancel")

	content := lipgloss.JoinVertical(lipgloss.Left,
		"",
		titleStyle.Render(d.Title),
		"",
		strings.Join(rows, "\n"),
		"",
		hint,
		"",
	)

	dialogStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(t.Accent).
		Padding(0, 2).
		Width(d.Width)

	return dialogStyle.Render(content)
}

type ConfirmModel struct {
	Title   string
	Message string
	Width   int
	Yes     bool
	Done    bool
}

func NewConfirm(title, message string, width int) ConfirmModel {
	return ConfirmModel{
		Title:   title,
		Message: message,
		Width:   width,
	}
}

func (c ConfirmModel) Update(msg tea.Msg) (ConfirmModel, tea.Cmd) {
	if keyMsg, ok := msg.(tea.KeyMsg); ok {
		switch keyMsg.String() {
		case "y", "Y":
			c.Yes = true
			c.Done = true
		case "n", "N", "esc":
			c.Yes = false
			c.Done = true
		case "left", "right", "tab", "h", "l":
			c.Yes = !c.Yes
		case "enter":
			c.Done = true
		}
	}
	return c, nil
}

func (c ConfirmModel) View() string {
	t := theme.Active

	titleStyle := lipgloss.NewStyle().
		Foreground(t.Accent).
		Bold(true).
		Padding(0, 1)

	msgStyle := lipgloss.NewStyle().
		Foreground(t.LightForeground).
		Padding(0, 2)

	yesStyle := lipgloss.NewStyle().Padding(0, 2)
	noStyle := lipgloss.NewStyle().Padding(0, 2)

	if c.Yes {
		yesStyle = yesStyle.Background(t.Green).Foreground(t.DarkerBackground).Bold(true)
		noStyle = noStyle.Foreground(t.DarkForeground)
	} else {
		yesStyle = yesStyle.Foreground(t.DarkForeground)
		noStyle = noStyle.Background(t.Red).Foreground(t.DarkerBackground).Bold(true)
	}

	buttons := lipgloss.JoinHorizontal(lipgloss.Center,
		yesStyle.Render("  Yes  "),
		"  ",
		noStyle.Render("  No  "),
	)

	content := lipgloss.JoinVertical(lipgloss.Center,
		"",
		titleStyle.Render(c.Title),
		"",
		msgStyle.Render(c.Message),
		"",
		buttons,
		"",
	)

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(t.Yellow).
		Padding(0, 2).
		Width(c.Width).
		Render(content)
}

type SelectOption struct {
	Label       string
	Description string
	Value       string
}

type SelectModel struct {
	Title    string
	Options  []SelectOption
	Cursor   int
	Width    int
	Done     bool
	Canceled bool
}

func NewSelect(title string, options []SelectOption, width int) SelectModel {
	return SelectModel{
		Title:   title,
		Options: options,
		Width:   width,
	}
}

func (s SelectModel) Update(msg tea.Msg) (SelectModel, tea.Cmd) {
	if keyMsg, ok := msg.(tea.KeyMsg); ok {
		switch keyMsg.String() {
		case "up", "k":
			if s.Cursor > 0 {
				s.Cursor--
			}
		case "down", "j":
			if s.Cursor < len(s.Options)-1 {
				s.Cursor++
			}
		case "enter":
			s.Done = true
		case "esc":
			s.Canceled = true
			s.Done = true
		}
	}
	return s, nil
}

func (s SelectModel) Selected() SelectOption {
	if s.Cursor < len(s.Options) {
		return s.Options[s.Cursor]
	}
	return SelectOption{}
}

func (s SelectModel) View() string {
	t := theme.Active

	titleStyle := lipgloss.NewStyle().
		Foreground(t.Accent).
		Bold(true).
		Padding(0, 1)

	var rows []string
	for i, opt := range s.Options {
		cursor := "  "
		style := lipgloss.NewStyle().Foreground(t.Foreground)
		descStyle := lipgloss.NewStyle().Foreground(t.DarkForeground)

		if i == s.Cursor {
			cursor = lipgloss.NewStyle().Foreground(t.Accent).Render("▸ ")
			style = style.Foreground(t.Accent).Bold(true)
			descStyle = descStyle.Foreground(t.LightForeground)
		}

		row := cursor + style.Render(opt.Label)
		if opt.Description != "" {
			row += " " + descStyle.Render(opt.Description)
		}
		rows = append(rows, row)
	}

	content := lipgloss.JoinVertical(lipgloss.Left,
		"",
		titleStyle.Render(fmt.Sprintf("  %s", s.Title)),
		"",
		strings.Join(rows, "\n"),
		"",
		lipgloss.NewStyle().Foreground(t.DarkForeground).Italic(true).Render("  ↑↓:navigate  enter:select  esc:cancel"),
		"",
	)

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(t.Accent).
		Padding(0, 2).
		Width(s.Width).
		Render(content)
}
