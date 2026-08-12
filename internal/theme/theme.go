package theme

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/charmbracelet/lipgloss"
	"github.com/lucasb-eyer/go-colorful"
)

type OmarchyColors struct {
	Mode              string `toml:"mode"`
	Accent            string `toml:"accent"`
	Cursor            string `toml:"cursor"`
	Selection         string `toml:"selection"`
	SelectionBg       string `toml:"selection_background"`
	SelectionFg       string `toml:"selection_foreground"`
	Muted             string `toml:"muted"`
	Background        string `toml:"background"`
	DarkBackground    string `toml:"dark_background"`
	DarkerBackground  string `toml:"darker_background"`
	LighterBackground string `toml:"lighter_background"`
	Foreground        string `toml:"foreground"`
	DarkForeground    string `toml:"dark_foreground"`
	LightForeground   string `toml:"light_foreground"`
	BrightForeground  string `toml:"bright_foreground"`
	Red               string `toml:"red"`
	Yellow            string `toml:"yellow"`
	Orange            string `toml:"orange"`
	Green             string `toml:"green"`
	Cyan              string `toml:"cyan"`
	Blue              string `toml:"blue"`
	Magenta           string `toml:"magenta"`
	Brown             string `toml:"brown"`
	BrightRed         string `toml:"bright_red"`
	BrightYellow      string `toml:"bright_yellow"`
	BrightGreen       string `toml:"bright_green"`
	BrightCyan        string `toml:"bright_cyan"`
	BrightBlue        string `toml:"bright_blue"`
	BrightMagenta     string `toml:"bright_magenta"`
	// Numbered color format (used by generated theme configs)
	Color0  string `toml:"color0"`
	Color1  string `toml:"color1"`
	Color2  string `toml:"color2"`
	Color3  string `toml:"color3"`
	Color4  string `toml:"color4"`
	Color5  string `toml:"color5"`
	Color6  string `toml:"color6"`
	Color7  string `toml:"color7"`
	Color8  string `toml:"color8"`
	Color9  string `toml:"color9"`
	Color10 string `toml:"color10"`
	Color11 string `toml:"color11"`
	Color12 string `toml:"color12"`
	Color13 string `toml:"color13"`
	Color14 string `toml:"color14"`
	Color15 string `toml:"color15"`
}

type Theme struct {
	Accent           lipgloss.Color
	Selection        lipgloss.Color
	Muted            lipgloss.Color
	Background       lipgloss.Color
	DarkBackground   lipgloss.Color
	DarkerBackground lipgloss.Color
	LighterBg        lipgloss.Color
	Foreground       lipgloss.Color
	DarkForeground   lipgloss.Color
	LightForeground  lipgloss.Color
	BrightForeground lipgloss.Color
	Red              lipgloss.Color
	Yellow           lipgloss.Color
	Orange           lipgloss.Color
	Green            lipgloss.Color
	Cyan             lipgloss.Color
	Blue             lipgloss.Color
	Magenta          lipgloss.Color
	Brown            lipgloss.Color
	BrightRed        lipgloss.Color
	BrightYellow     lipgloss.Color
	BrightGreen      lipgloss.Color
	BrightCyan       lipgloss.Color
	BrightBlue       lipgloss.Color
	BrightMagenta    lipgloss.Color
	IsDark           bool

	RainbowColors []lipgloss.Color
	GradientPairs []GradientPair
}

type GradientPair struct {
	From, To colorful.Color
}

var Active *Theme

func init() {
	Active = Load()
}

func Load() *Theme {
	if oc := loadOmarchy(); oc != nil {
		return fromOmarchy(oc)
	}
	return defaultTheme()
}

func loadOmarchy() *OmarchyColors {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}

	activeTheme := filepath.Join(home, ".local", "state", "omarchy", "current", "theme", "colors.toml")
	if _, err := os.Stat(activeTheme); err == nil {
		var oc OmarchyColors
		if _, err := toml.DecodeFile(activeTheme, &oc); err == nil {
			return &oc
		}
	}

	paths := []string{
		filepath.Join(home, ".config", "omarchy", "themes"),
		filepath.Join(home, ".local", "share", "omarchy", "themes"),
	}

	for _, base := range paths {
		entries, err := os.ReadDir(base)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			colorsFile := filepath.Join(base, e.Name(), "colors.toml")
			if _, err := os.Stat(colorsFile); err == nil {
				var oc OmarchyColors
				if _, err := toml.DecodeFile(colorsFile, &oc); err == nil {
					return &oc
				}
			}
		}
	}
	return nil
}

func (oc *OmarchyColors) normalize() {
	// Map numbered colors to named colors when named aren't present
	// Standard terminal color mapping: 0=black, 1=red, 2=green, 3=yellow,
	// 4=blue, 5=magenta, 6=cyan, 7=white, 8-15=bright variants
	or := func(a, b string) string {
		if a != "" {
			return a
		}
		return b
	}
	oc.Red = or(oc.Red, oc.Color1)
	oc.Green = or(oc.Green, oc.Color2)
	oc.Yellow = or(oc.Yellow, oc.Color3)
	oc.Blue = or(oc.Blue, oc.Color4)
	oc.Magenta = or(oc.Magenta, oc.Color5)
	oc.Cyan = or(oc.Cyan, oc.Color6)
	oc.BrightRed = or(oc.BrightRed, oc.Color9)
	oc.BrightGreen = or(oc.BrightGreen, oc.Color10)
	oc.BrightYellow = or(oc.BrightYellow, oc.Color11)
	oc.BrightBlue = or(oc.BrightBlue, oc.Color12)
	oc.BrightMagenta = or(oc.BrightMagenta, oc.Color13)
	oc.BrightCyan = or(oc.BrightCyan, oc.Color14)

	oc.Orange = or(oc.Orange, oc.Color3) // fallback to yellow
	oc.Brown = or(oc.Brown, oc.Color7)   // fallback to white/gray

	oc.Selection = or(oc.Selection, oc.SelectionBg)
	oc.DarkForeground = or(oc.DarkForeground, oc.Color8)
	oc.LightForeground = or(oc.LightForeground, oc.Color7)
	oc.BrightForeground = or(oc.BrightForeground, oc.Color15)
	oc.Muted = or(oc.Muted, oc.Color8)

	// Derive background variants if not set
	if oc.DarkBackground == "" {
		oc.DarkBackground = or(oc.DarkBackground, darken(oc.Background, 0.1))
	}
	if oc.DarkerBackground == "" {
		oc.DarkerBackground = or(oc.DarkerBackground, darken(oc.Background, 0.2))
	}
	if oc.LighterBackground == "" {
		oc.LighterBackground = or(oc.LighterBackground, lighten(oc.Background, 0.1))
	}

	// Detect mode from background luminance if not set
	if oc.Mode == "" {
		if c, err := colorful.Hex(oc.Background); err == nil {
			_, _, l := c.Hsl()
			if l > 0.5 {
				oc.Mode = "light"
			} else {
				oc.Mode = "dark"
			}
		}
	}
}

func darken(hex string, amount float64) string {
	c, err := colorful.Hex(hex)
	if err != nil {
		return hex
	}
	h, s, l := c.Hsl()
	l = math.Max(0, l-amount)
	return colorful.Hsl(h, s, l).Hex()
}

func lighten(hex string, amount float64) string {
	c, err := colorful.Hex(hex)
	if err != nil {
		return hex
	}
	h, s, l := c.Hsl()
	l = math.Min(1, l+amount)
	return colorful.Hsl(h, s, l).Hex()
}

func fromOmarchy(oc *OmarchyColors) *Theme {
	oc.normalize()
	t := &Theme{
		Accent:           lipgloss.Color(oc.Accent),
		Selection:        lipgloss.Color(oc.Selection),
		Muted:            lipgloss.Color(oc.Muted),
		Background:       lipgloss.Color(oc.Background),
		DarkBackground:   lipgloss.Color(oc.DarkBackground),
		DarkerBackground: lipgloss.Color(oc.DarkerBackground),
		LighterBg:        lipgloss.Color(oc.LighterBackground),
		Foreground:       lipgloss.Color(oc.Foreground),
		DarkForeground:   lipgloss.Color(oc.DarkForeground),
		LightForeground:  lipgloss.Color(oc.LightForeground),
		BrightForeground: lipgloss.Color(oc.BrightForeground),
		Red:              lipgloss.Color(oc.Red),
		Yellow:           lipgloss.Color(oc.Yellow),
		Orange:           lipgloss.Color(oc.Orange),
		Green:            lipgloss.Color(oc.Green),
		Cyan:             lipgloss.Color(oc.Cyan),
		Blue:             lipgloss.Color(oc.Blue),
		Magenta:          lipgloss.Color(oc.Magenta),
		Brown:            lipgloss.Color(oc.Brown),
		BrightRed:        lipgloss.Color(oc.BrightRed),
		BrightYellow:     lipgloss.Color(oc.BrightYellow),
		BrightGreen:      lipgloss.Color(oc.BrightGreen),
		BrightCyan:       lipgloss.Color(oc.BrightCyan),
		BrightBlue:       lipgloss.Color(oc.BrightBlue),
		BrightMagenta:    lipgloss.Color(oc.BrightMagenta),
		IsDark:           oc.Mode == "dark",
	}
	t.buildRainbow()
	t.buildGradients()
	return t
}

func defaultTheme() *Theme {
	t := &Theme{
		Accent:           lipgloss.Color("#89b4fa"),
		Selection:        lipgloss.Color("#45475a"),
		Muted:            lipgloss.Color("#585b70"),
		Background:       lipgloss.Color("#1e1e2e"),
		DarkBackground:   lipgloss.Color("#161622"),
		DarkerBackground: lipgloss.Color("#101019"),
		LighterBg:        lipgloss.Color("#313244"),
		Foreground:       lipgloss.Color("#cdd6f4"),
		DarkForeground:   lipgloss.Color("#6c7086"),
		LightForeground:  lipgloss.Color("#bac2de"),
		BrightForeground: lipgloss.Color("#cdd6f4"),
		Red:              lipgloss.Color("#f38ba8"),
		Yellow:           lipgloss.Color("#f9e2af"),
		Orange:           lipgloss.Color("#f6b6ab"),
		Green:            lipgloss.Color("#a6e3a1"),
		Cyan:             lipgloss.Color("#94e2d5"),
		Blue:             lipgloss.Color("#89b4fa"),
		Magenta:          lipgloss.Color("#f5c2e7"),
		Brown:            lipgloss.Color("#7b5b55"),
		BrightRed:        lipgloss.Color("#f38ba8"),
		BrightYellow:     lipgloss.Color("#f9e2af"),
		BrightGreen:      lipgloss.Color("#a6e3a1"),
		BrightCyan:       lipgloss.Color("#94e2d5"),
		BrightBlue:       lipgloss.Color("#89b4fa"),
		BrightMagenta:    lipgloss.Color("#f5c2e7"),
		IsDark:           true,
	}
	t.buildRainbow()
	t.buildGradients()
	return t
}

func (t *Theme) buildRainbow() {
	t.RainbowColors = []lipgloss.Color{
		t.Red, t.Orange, t.Yellow, t.Green, t.Cyan, t.Blue, t.Magenta,
	}
}

func (t *Theme) buildGradients() {
	colors := []string{
		string(t.Magenta), string(t.Blue), string(t.Cyan),
		string(t.Green), string(t.Yellow), string(t.Orange), string(t.Red),
	}
	for i := 0; i < len(colors)-1; i++ {
		from, _ := colorful.Hex(colors[i])
		to, _ := colorful.Hex(colors[i+1])
		t.GradientPairs = append(t.GradientPairs, GradientPair{From: from, To: to})
	}
}

func (t *Theme) Rainbow(text string) string {
	if len(text) == 0 {
		return ""
	}
	runes := []rune(text)
	var sb strings.Builder
	for i, r := range runes {
		if r == ' ' || r == '\n' {
			sb.WriteRune(r)
			continue
		}
		color := t.rainbowColor(float64(i) / float64(len(runes)))
		sb.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(color.Hex())).Render(string(r)))
	}
	return sb.String()
}

func (t *Theme) RainbowWithOffset(text string, offset float64) string {
	if len(text) == 0 {
		return ""
	}
	runes := []rune(text)
	var sb strings.Builder
	for i, r := range runes {
		if r == ' ' || r == '\n' {
			sb.WriteRune(r)
			continue
		}
		pos := math.Mod(float64(i)/float64(len(runes))+offset, 1.0)
		color := t.rainbowColor(pos)
		sb.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(color.Hex())).Render(string(r)))
	}
	return sb.String()
}

func (t *Theme) Gradient(text string, from, to lipgloss.Color) string {
	if len(text) == 0 {
		return ""
	}
	c1, _ := colorful.Hex(string(from))
	c2, _ := colorful.Hex(string(to))
	runes := []rune(text)
	var sb strings.Builder
	for i, r := range runes {
		if r == ' ' || r == '\n' {
			sb.WriteRune(r)
			continue
		}
		ratio := float64(i) / float64(max(len(runes)-1, 1))
		c := c1.BlendHcl(c2, ratio)
		sb.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(c.Hex())).Render(string(r)))
	}
	return sb.String()
}

func (t *Theme) RainbowColor(position float64) string {
	return t.rainbowColor(position).Hex()
}

func (t *Theme) rainbowColor(position float64) colorful.Color {
	totalPairs := len(t.GradientPairs)
	if totalPairs == 0 {
		c, _ := colorful.Hex(string(t.Accent))
		return c
	}
	scaled := position * float64(totalPairs)
	idx := int(scaled)
	if idx >= totalPairs {
		idx = totalPairs - 1
	}
	frac := scaled - float64(idx)
	pair := t.GradientPairs[idx]
	return pair.From.BlendHcl(pair.To, frac)
}

func GradientBar(width int, t *Theme) string {
	var sb strings.Builder
	for i := range width {
		pos := float64(i) / float64(max(width-1, 1))
		c := t.rainbowColor(pos)
		sb.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(c.Hex())).Render("━"))
	}
	return sb.String()
}

func SparkleText(text string, t *Theme) string {
	sparkles := []string{"✦", "✧", "⋆", "˚", "⊹"}
	runes := []rune(text)
	var sb strings.Builder
	sparkIdx := 0
	for i, r := range runes {
		if r == ' ' {
			sb.WriteRune(r)
			continue
		}
		pos := float64(i) / float64(max(len(runes)-1, 1))
		c := t.rainbowColor(pos)
		style := lipgloss.NewStyle().Foreground(lipgloss.Color(c.Hex())).Bold(true)
		sb.WriteString(style.Render(string(r)))
		if i > 0 && i%8 == 0 {
			sparkle := sparkles[sparkIdx%len(sparkles)]
			sparkIdx++
			sc := t.rainbowColor(math.Mod(pos+0.3, 1.0))
			sb.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(sc.Hex())).Render(sparkle))
		}
	}
	return sb.String()
}

func RainbowBorder(width, height int, t *Theme) string {
	topBottom := func(w int, offset float64) string {
		var sb strings.Builder
		for i := range w {
			pos := math.Mod(float64(i)/float64(max(w-1, 1))+offset, 1.0)
			c := t.rainbowColor(pos)
			sb.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(c.Hex())).Render("━"))
		}
		return sb.String()
	}

	lines := make([]string, height)

	lines[0] = fmt.Sprintf("%s%s%s",
		lipgloss.NewStyle().Foreground(t.RainbowColors[0]).Render("┏"),
		topBottom(width-2, 0),
		lipgloss.NewStyle().Foreground(t.RainbowColors[len(t.RainbowColors)-1]).Render("┓"))

	for i := 1; i < height-1; i++ {
		pos := float64(i) / float64(max(height-1, 1))
		lc := t.rainbowColor(pos)
		rc := t.rainbowColor(1.0 - pos)
		lines[i] = fmt.Sprintf("%s%s%s",
			lipgloss.NewStyle().Foreground(lipgloss.Color(lc.Hex())).Render("┃"),
			strings.Repeat(" ", width-2),
			lipgloss.NewStyle().Foreground(lipgloss.Color(rc.Hex())).Render("┃"))
	}

	lines[height-1] = fmt.Sprintf("%s%s%s",
		lipgloss.NewStyle().Foreground(t.RainbowColors[len(t.RainbowColors)-1]).Render("┗"),
		topBottom(width-2, 0.5),
		lipgloss.NewStyle().Foreground(t.RainbowColors[0]).Render("┛"))

	return strings.Join(lines, "\n")
}
