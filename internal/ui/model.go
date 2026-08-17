package ui

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/z19r/smbark/internal/smb"
	"github.com/z19r/smbark/internal/theme"
	"github.com/z19r/smbark/internal/ui/components"
)

const (
	TabDiscover  = 0
	TabMounted   = 1
	TabAutomount = 2
	TabConfig    = 3
)

type ViewState int

const (
	ViewMain ViewState = iota
	ViewHostShares
	ViewMountDialog
	ViewUnmountConfirm
	ViewAutomountDialog
	ViewAutomountRemoveConfirm
	ViewCredentials
	ViewHelp
	ViewConnectivity
	ViewMountOptions
	ViewAddHost
)

type Model struct {
	width  int
	height int
	frame  float64

	activeTab int
	viewState ViewState

	spinner  spinner.Model
	progress progress.Model

	hosts         []smb.Host
	hostShares    map[string][]smb.Share
	mountedShares []smb.Share
	automounts    []smb.AutomountConfig
	fstabEntries  []smb.Share

	selectedHost  string
	selectedShare *smb.Share

	hostList      list.Model
	shareList     list.Model
	mountedList   list.Model
	automountList list.Model

	dialog     *components.DialogModel
	confirm    *components.ConfirmModel
	selectMenu *components.SelectModel

	scanning     bool
	mounting     bool
	statusMsg    string
	statusErr    bool
	statusExpiry time.Time
	pendingRetry tea.Cmd

	connectivity map[string]ConnStatus

	helpViewport viewport.Model
}

type ConnStatus struct {
	Latency time.Duration
	Err     error
	Checked time.Time
}

func NewModel() Model {
	t := theme.Active

	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(t.Accent)

	p := progress.New(
		progress.WithScaledGradient(string(t.Blue), string(t.Magenta)),
		progress.WithoutPercentage(),
	)

	m := Model{
		spinner:      s,
		progress:     p,
		hostShares:   make(map[string][]smb.Share),
		connectivity: make(map[string]ConnStatus),
	}
	return m
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(
		m.spinner.Tick,
		tickCmd(),
		loadMountedShares,
		loadAutomounts,
		loadFstabEntries,
	)
}

func tickCmd() tea.Cmd {
	return tea.Tick(time.Millisecond*50, func(t time.Time) tea.Msg {
		return TickMsg(t)
	})
}

func loadMountedShares() tea.Msg {
	shares, err := smb.GetMountedShares()
	return MountedSharesMsg{Shares: shares, Err: err}
}

func loadAutomounts() tea.Msg {
	configs, err := smb.GetAutomounts()
	return AutomountsMsg{Configs: configs, Err: err}
}

func loadFstabEntries() tea.Msg {
	shares, err := smb.GetFstabEntries()
	return FstabEntriesMsg{Shares: shares, Err: err}
}

func discoverHosts() tea.Msg {
	hosts, err := smb.DiscoverHosts(context.Background())
	return HostsDiscoveredMsg{Hosts: hosts, Err: err}
}

func listShares(host string, creds smb.Credentials) tea.Cmd {
	return func() tea.Msg {
		shares, err := smb.ListShares(context.Background(), host, creds)
		return SharesListedMsg{Host: host, Shares: shares, Err: err}
	}
}

func mountShare(share smb.Share, opts smb.MountOptions) tea.Cmd {
	return func() tea.Msg {
		err := smb.MountShare(share, opts)
		return MountResultMsg{Share: share, Err: err}
	}
}

func unmountShare(mountPoint string) tea.Cmd {
	return func() tea.Msg {
		err := smb.UnmountShare(mountPoint)
		return UnmountResultMsg{MountPoint: mountPoint, Err: err}
	}
}

func createAutomount(cfg smb.AutomountConfig) tea.Cmd {
	return func() tea.Msg {
		err := smb.CreateAutomount(cfg)
		return AutomountCreateMsg{Share: cfg.Share, Err: err}
	}
}

func removeAutomount(mountPoint string) tea.Cmd {
	return func() tea.Msg {
		err := smb.RemoveAutomount(mountPoint)
		return AutomountRemoveMsg{MountPoint: mountPoint, Err: err}
	}
}

func checkConnectivity(host string) tea.Cmd {
	return func() tea.Msg {
		latency, err := smb.CheckConnectivity(host)
		return ConnectivityMsg{Host: host, Latency: latency, Err: err}
	}
}

func (m *Model) setStatus(msg string, isErr bool) {
	m.statusMsg = msg
	m.statusErr = isErr
	m.statusExpiry = time.Now().Add(5 * time.Second)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.rebuildLists()
		return m, nil

	case TickMsg:
		m.frame++
		if !m.statusExpiry.IsZero() && time.Now().After(m.statusExpiry) {
			m.statusMsg = ""
			m.statusErr = false
		}
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		cmds = append(cmds, cmd, tickCmd())
		return m, tea.Batch(cmds...)

	case tea.KeyMsg:
		if m.dialog != nil {
			d, cmd := m.dialog.Update(msg)
			m.dialog = &d
			if d.Done {
				return m.handleDialogDone()
			}
			return m, cmd
		}
		if m.confirm != nil {
			c, cmd := m.confirm.Update(msg)
			m.confirm = &c
			if c.Done {
				return m.handleConfirmDone()
			}
			return m, cmd
		}
		if m.selectMenu != nil {
			s, cmd := m.selectMenu.Update(msg)
			m.selectMenu = &s
			if s.Done {
				return m.handleSelectDone()
			}
			return m, cmd
		}

		switch msg.String() {
		case "ctrl+c", "q":
			if m.viewState == ViewMain {
				return m, tea.Quit
			}
			m.viewState = ViewMain
			m.selectedHost = ""
			m.selectedShare = nil
			return m, nil
		case "tab":
			m.activeTab = (m.activeTab + 1) % len(Tabs)
			return m, nil
		case "shift+tab":
			m.activeTab = (m.activeTab - 1 + len(Tabs)) % len(Tabs)
			return m, nil
		case "?":
			if m.viewState == ViewHelp {
				m.viewState = ViewMain
			} else {
				m.viewState = ViewHelp
				m.buildHelpView()
			}
			return m, nil
		case "esc":
			if m.viewState != ViewMain {
				m.viewState = ViewMain
				m.selectedHost = ""
				m.selectedShare = nil
				return m, nil
			}
		}

		return m.handleTabKeyMsg(msg)

	case HostsDiscoveredMsg:
		m.scanning = false
		if msg.Err != nil {
			m.setStatus(fmt.Sprintf("Discovery error: %v", msg.Err), true)
			return m, nil
		}
		m.hosts = msg.Hosts
		m.rebuildLists()
		m.setStatus(fmt.Sprintf("Found %d host(s) on the network", len(m.hosts)), false)
		return m, nil

	case SharesListedMsg:
		m.scanning = false
		if msg.Err != nil {
			m.setStatus(fmt.Sprintf("Error listing shares on %s: %v", msg.Host, msg.Err), true)
			return m, nil
		}
		m.hostShares[msg.Host] = msg.Shares
		m.selectedHost = msg.Host
		m.viewState = ViewHostShares
		m.rebuildLists()
		m.setStatus(fmt.Sprintf("Found %d share(s) on %s", len(msg.Shares), msg.Host), false)
		return m, nil

	case MountedSharesMsg:
		if msg.Err == nil {
			m.mountedShares = msg.Shares
			m.rebuildLists()
		}
		return m, nil

	case AutomountsMsg:
		if msg.Err == nil {
			m.automounts = msg.Configs
			m.rebuildLists()
		}
		return m, nil

	case FstabEntriesMsg:
		if msg.Err == nil {
			m.fstabEntries = msg.Shares
		}
		return m, nil

	case MountResultMsg:
		m.mounting = false
		if msg.Err != nil {
			if m.handleSudoError(msg.Err, mountShare(msg.Share, smb.DefaultMountOptions())) {
				return m, m.promptSudo()
			}
			m.setStatus(fmt.Sprintf("Mount failed: %v", msg.Err), true)
			return m, nil
		}
		m.setStatus(fmt.Sprintf("Successfully mounted %s", msg.Share.Path), false)
		m.viewState = ViewMain
		return m, tea.Batch(loadMountedShares, loadAutomounts)

	case UnmountResultMsg:
		if msg.Err != nil {
			if m.handleSudoError(msg.Err, unmountShare(msg.MountPoint)) {
				return m, m.promptSudo()
			}
			m.setStatus(fmt.Sprintf("Unmount failed: %v", msg.Err), true)
			return m, nil
		}
		m.setStatus(fmt.Sprintf("Unmounted %s", msg.MountPoint), false)
		return m, loadMountedShares

	case AutomountCreateMsg:
		if msg.Err != nil {
			if m.handleSudoError(msg.Err, nil) {
				return m, m.promptSudo()
			}
			m.setStatus(fmt.Sprintf("Automount setup failed: %v", msg.Err), true)
			return m, nil
		}
		m.setStatus(fmt.Sprintf("Automount configured for %s", msg.Share.Path), false)
		return m, tea.Batch(loadMountedShares, loadAutomounts)

	case AutomountRemoveMsg:
		if msg.Err != nil {
			if m.handleSudoError(msg.Err, removeAutomount(msg.MountPoint)) {
				return m, m.promptSudo()
			}
			m.setStatus(fmt.Sprintf("Remove automount failed: %v", msg.Err), true)
			return m, nil
		}
		m.setStatus(fmt.Sprintf("Removed automount for %s", msg.MountPoint), false)
		return m, tea.Batch(loadMountedShares, loadAutomounts)

	case SudoRefreshedMsg:
		if msg.Err != nil {
			m.setStatus("sudo authentication failed", true)
			return m, nil
		}
		m.setStatus("Authenticated — retrying...", false)
		if m.pendingRetry != nil {
			retry := m.pendingRetry
			m.pendingRetry = nil
			return m, retry
		}
		return m, nil

	case ConnectivityMsg:
		m.connectivity[msg.Host] = ConnStatus{
			Latency: msg.Latency,
			Err:     msg.Err,
			Checked: time.Now(),
		}
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	}

	return m, tea.Batch(cmds...)
}

func (m *Model) handleTabKeyMsg(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.activeTab {
	case TabDiscover:
		return m.handleDiscoverKeys(msg)
	case TabMounted:
		return m.handleMountedKeys(msg)
	case TabAutomount:
		return m.handleAutomountKeys(msg)
	case TabConfig:
		return m.handleConfigKeys(msg)
	}
	return m, nil
}

func (m *Model) handleDiscoverKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "s", "r":
		if !m.scanning {
			m.scanning = true
			m.setStatus("Scanning network for SMB hosts...", false)
			return m, discoverHosts
		}
	case "a":
		if m.viewState == ViewMain {
			d := components.NewDialog("Add Host", []components.DialogField{
				{Label: "Host", Placeholder: "IP address or hostname"},
			}, min(m.width-10, 50))
			m.dialog = &d
			m.viewState = ViewAddHost
			return m, nil
		}
	case "enter":
		if m.viewState == ViewHostShares {
			return m.handleShareSelect()
		}
		if len(m.hosts) > 0 {
			idx := m.hostList.Index()
			if idx < len(m.hosts) {
				host := m.hosts[idx]
				target := host.IP
				if target == "" {
					target = host.Name
				}
				m.scanning = true
				m.setStatus(fmt.Sprintf("Listing shares on %s...", target), false)
				return m, listShares(target, smb.Credentials{})
			}
		}
	case "c":
		if m.viewState == ViewHostShares && m.selectedHost != "" {
			return m, checkConnectivity(m.selectedHost)
		}
	case "m":
		if m.viewState == ViewHostShares {
			return m.handleShareSelect()
		}
	}

	if m.viewState == ViewHostShares {
		var cmd tea.Cmd
		m.shareList, cmd = m.shareList.Update(msg)
		return m, cmd
	}

	var cmd tea.Cmd
	m.hostList, cmd = m.hostList.Update(msg)
	return m, cmd
}

func (m *Model) handleShareSelect() (tea.Model, tea.Cmd) {
	shares := m.hostShares[m.selectedHost]
	if len(shares) == 0 {
		return m, nil
	}
	idx := m.shareList.Index()
	if idx >= len(shares) {
		return m, nil
	}
	share := shares[idx]
	m.selectedShare = &share

	s := components.NewSelect("Action", []components.SelectOption{
		{Label: "Mount Share", Description: "Mount this share now", Value: "mount"},
		{Label: "Mount with Options", Description: "Configure mount options", Value: "mount-opts"},
		{Label: "Setup Automount", Description: "Configure systemd automount", Value: "automount"},
		{Label: "Check Connectivity", Description: "Test connection to host", Value: "connectivity"},
	}, min(m.width-10, 60))
	m.selectMenu = &s
	return m, nil
}

func (m *Model) handleMountedKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "r":
		return m, loadMountedShares
	case "u":
		if len(m.mountedShares) > 0 {
			idx := m.mountedList.Index()
			if idx < len(m.mountedShares) {
				share := m.mountedShares[idx]
				c := components.NewConfirm("Unmount Share",
					fmt.Sprintf("Unmount %s from %s?", share.Path, share.MountPoint),
					min(m.width-10, 50))
				m.confirm = &c
				m.selectedShare = &share
				m.viewState = ViewUnmountConfirm
			}
		}
	case "f":
		if len(m.mountedShares) > 0 {
			idx := m.mountedList.Index()
			if idx < len(m.mountedShares) {
				share := m.mountedShares[idx]
				c := components.NewConfirm("Force Unmount",
					fmt.Sprintf("Force unmount %s? (lazy unmount)", share.MountPoint),
					min(m.width-10, 50))
				c.Yes = false
				m.confirm = &c
				m.selectedShare = &share
				m.viewState = ViewUnmountConfirm
			}
		}
	}

	var cmd tea.Cmd
	m.mountedList, cmd = m.mountedList.Update(msg)
	return m, cmd
}

func (m *Model) handleAutomountKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "r":
		return m, loadAutomounts
	case "d", "delete":
		if len(m.automounts) > 0 {
			idx := m.automountList.Index()
			if idx < len(m.automounts) {
				cfg := m.automounts[idx]
				c := components.NewConfirm("Remove Automount",
					fmt.Sprintf("Remove automount for %s?", cfg.Share.Path),
					min(m.width-10, 50))
				m.confirm = &c
				m.viewState = ViewAutomountRemoveConfirm
			}
		}
	}

	var cmd tea.Cmd
	m.automountList, cmd = m.automountList.Update(msg)
	return m, cmd
}

func (m *Model) handleConfigKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "r":
		return m, tea.Batch(loadMountedShares, loadAutomounts, loadFstabEntries)
	}
	return m, nil
}

func (m *Model) handleDialogDone() (tea.Model, tea.Cmd) {
	if m.dialog == nil {
		return m, nil
	}
	d := m.dialog
	m.dialog = nil

	if d.Canceled {
		m.viewState = ViewMain
		return m, nil
	}

	vals := d.Values()

	switch m.viewState {
	case ViewAddHost:
		host := vals["Host"]
		if host != "" {
			m.hosts = append(m.hosts, smb.Host{Name: host, IP: host})
			m.rebuildLists()
			m.scanning = true
			m.setStatus(fmt.Sprintf("Listing shares on %s...", host), false)
			m.viewState = ViewMain
			return m, listShares(host, smb.Credentials{})
		}
		m.viewState = ViewMain
		return m, nil

	case ViewCredentials:
		if m.selectedHost != "" {
			creds := smb.Credentials{
				Username: vals["Username"],
				Password: vals["Password"],
				Domain:   vals["Domain"],
			}
			m.scanning = true
			m.setStatus(fmt.Sprintf("Listing shares on %s...", m.selectedHost), false)
			return m, listShares(m.selectedHost, creds)
		}

	case ViewMountDialog:
		if m.selectedShare != nil {
			opts := smb.DefaultMountOptions()
			opts.MountPoint = vals["Mount Point"]
			opts.Creds = smb.Credentials{
				Username: vals["Username"],
				Password: vals["Password"],
			}
			m.mounting = true
			m.setStatus(fmt.Sprintf("Mounting %s...", m.selectedShare.Path), false)
			return m, mountShare(*m.selectedShare, opts)
		}

	case ViewAutomountDialog:
		if m.selectedShare != nil {
			opts := smb.DefaultMountOptions()
			opts.MountPoint = vals["Mount Point"]
			opts.Creds = smb.Credentials{
				Username: vals["Username"],
				Password: vals["Password"],
			}
			cfg := smb.AutomountConfig{
				Share:      *m.selectedShare,
				MountPoint: vals["Mount Point"],
				Options:    opts,
			}
			m.setStatus(fmt.Sprintf("Setting up automount for %s...", m.selectedShare.Path), false)
			return m, createAutomount(cfg)
		}

	case ViewMountOptions:
		if m.selectedShare != nil {
			opts := smb.MountOptions{
				Version:    vals["SMB Version"],
				Security:   vals["Security"],
				MountPoint: vals["Mount Point"],
				UID:        vals["UID"],
				GID:        vals["GID"],
				FileMode:   vals["File Mode"],
				DirMode:    vals["Dir Mode"],
				Creds: smb.Credentials{
					Username: vals["Username"],
					Password: vals["Password"],
					Domain:   vals["Domain"],
				},
			}
			m.mounting = true
			m.setStatus(fmt.Sprintf("Mounting %s...", m.selectedShare.Path), false)
			return m, mountShare(*m.selectedShare, opts)
		}
	}

	m.viewState = ViewMain
	return m, nil
}

func (m *Model) handleConfirmDone() (tea.Model, tea.Cmd) {
	if m.confirm == nil {
		return m, nil
	}
	c := m.confirm
	m.confirm = nil

	if !c.Yes {
		m.viewState = ViewMain
		return m, nil
	}

	switch m.viewState {
	case ViewUnmountConfirm:
		if m.selectedShare != nil {
			mp := m.selectedShare.MountPoint
			m.selectedShare = nil
			m.viewState = ViewMain
			return m, unmountShare(mp)
		}

	case ViewAutomountRemoveConfirm:
		idx := m.automountList.Index()
		if idx < len(m.automounts) {
			mp := m.automounts[idx].MountPoint
			m.viewState = ViewMain
			return m, removeAutomount(mp)
		}
	}

	m.viewState = ViewMain
	return m, nil
}

func (m *Model) handleSelectDone() (tea.Model, tea.Cmd) {
	if m.selectMenu == nil {
		return m, nil
	}
	s := m.selectMenu
	m.selectMenu = nil

	if s.Canceled || m.selectedShare == nil {
		return m, nil
	}

	share := m.selectedShare
	switch s.Selected().Value {
	case "mount":
		defaultMP := fmt.Sprintf("/mnt/smb/%s/%s", share.Host, share.Name)
		d := components.NewDialog("Mount Share: "+share.Path, []components.DialogField{
			{Label: "Mount Point", Placeholder: defaultMP, Value: defaultMP},
			{Label: "Username", Placeholder: "guest (leave empty)"},
			{Label: "Password", Password: true},
		}, min(m.width-10, 60))
		m.dialog = &d
		m.viewState = ViewMountDialog

	case "mount-opts":
		defaultMP := fmt.Sprintf("/mnt/smb/%s/%s", share.Host, share.Name)
		defaults := smb.DefaultMountOptions()
		d := components.NewDialog("Mount Options: "+share.Path, []components.DialogField{
			{Label: "Mount Point", Value: defaultMP},
			{Label: "SMB Version", Value: defaults.Version, Placeholder: "auto, 3.0, 2.1, 1.0"},
			{Label: "Security", Value: defaults.Security, Placeholder: "ntlmssp, krb5"},
			{Label: "UID", Value: defaults.UID},
			{Label: "GID", Value: defaults.GID},
			{Label: "File Mode", Value: defaults.FileMode},
			{Label: "Dir Mode", Value: defaults.DirMode},
			{Label: "Username", Placeholder: "guest (leave empty)"},
			{Label: "Password", Password: true},
			{Label: "Domain", Placeholder: "WORKGROUP"},
		}, min(m.width-10, 65))
		m.dialog = &d
		m.viewState = ViewMountOptions

	case "automount":
		defaultMP := fmt.Sprintf("/mnt/smb/%s/%s", share.Host, share.Name)
		d := components.NewDialog("Setup Automount: "+share.Path, []components.DialogField{
			{Label: "Mount Point", Value: defaultMP},
			{Label: "Username", Placeholder: "guest (leave empty)"},
			{Label: "Password", Password: true},
		}, min(m.width-10, 60))
		m.dialog = &d
		m.viewState = ViewAutomountDialog

	case "connectivity":
		return m, checkConnectivity(share.Host)
	}

	return m, nil
}

func (m *Model) rebuildLists() {
	contentWidth := max(m.width-4, 20)
	contentHeight := max(m.height-16, 5)

	t := theme.Active

	// Host list
	var hostItems []list.Item
	for _, h := range m.hosts {
		label := h.Name
		if h.IP != "" {
			label += " (" + h.IP + ")"
		}
		status := ""
		if cs, ok := m.connectivity[h.IP]; ok {
			if cs.Err != nil {
				status = " ✗"
			} else {
				status = fmt.Sprintf(" ✓ %dms", cs.Latency.Milliseconds())
			}
		}
		hostItems = append(hostItems, simpleItem{title: "🖥  " + label + status, desc: h.Workgroup})
	}
	m.hostList = list.New(hostItems, list.NewDefaultDelegate(), contentWidth, contentHeight)
	m.hostList.Title = ""
	m.hostList.SetShowTitle(false)
	m.hostList.SetShowStatusBar(false)
	m.hostList.SetShowHelp(false)
	m.hostList.SetFilteringEnabled(true)
	m.hostList.Styles.FilterPrompt = lipgloss.NewStyle().Foreground(t.Accent)
	m.hostList.Styles.FilterCursor = lipgloss.NewStyle().Foreground(t.Accent)

	// Share list
	if m.selectedHost != "" {
		shares := m.hostShares[m.selectedHost]
		var shareItems []list.Item
		for _, s := range shares {
			shareItems = append(shareItems, components.ShareItem{Share: s})
		}
		delegate := components.NewShareListDelegate(t)
		m.shareList = list.New(shareItems, delegate, contentWidth, contentHeight)
		m.shareList.Title = ""
		m.shareList.SetShowTitle(false)
		m.shareList.SetShowStatusBar(false)
		m.shareList.SetShowHelp(false)
	}

	// Mounted list — include automounts that aren't already in the mounted list
	mountedPaths := make(map[string]bool)
	var mountedItems []list.Item
	for _, s := range m.mountedShares {
		mountedPaths[s.Path] = true
		for _, cfg := range m.automounts {
			if cfg.Share.Path == s.Path {
				s.IsAutomount = true
				break
			}
		}
		mountedItems = append(mountedItems, components.ShareItem{Share: s})
	}
	for _, cfg := range m.automounts {
		if mountedPaths[cfg.Share.Path] {
			continue
		}
		s := cfg.Share
		s.IsAutomount = true
		s.MountPoint = cfg.MountPoint
		mountedItems = append(mountedItems, components.ShareItem{Share: s})
	}
	delegate := components.NewShareListDelegate(t)
	m.mountedList = list.New(mountedItems, delegate, contentWidth, contentHeight)
	m.mountedList.Title = ""
	m.mountedList.SetShowTitle(false)
	m.mountedList.SetShowStatusBar(false)
	m.mountedList.SetShowHelp(false)

	// Automount list
	var automountItems []list.Item
	for _, cfg := range m.automounts {
		s := cfg.Share
		s.IsAutomount = true
		automountItems = append(automountItems, components.ShareItem{Share: s})
	}
	m.automountList = list.New(automountItems, delegate, contentWidth, contentHeight)
	m.automountList.Title = ""
	m.automountList.SetShowTitle(false)
	m.automountList.SetShowStatusBar(false)
	m.automountList.SetShowHelp(false)
}

func (m *Model) buildHelpView() {
	t := theme.Active
	helpContent := renderHelpContent(t)
	m.helpViewport = viewport.New(min(m.width-6, 70), max(m.height-18, 10))
	m.helpViewport.SetContent(helpContent)
}

type simpleItem struct {
	title string
	desc  string
}

func (s simpleItem) Title() string       { return s.title }
func (s simpleItem) Description() string { return s.desc }
func (s simpleItem) FilterValue() string { return s.title }

func (m Model) View() string {
	if m.width == 0 {
		return ""
	}

	header := components.RenderHeader(m.width, m.frame)
	tabs := RenderTabs(m.activeTab, m.width, m.frame)

	var content string

	if m.dialog != nil {
		content = lipgloss.Place(m.width, max(m.height-18, 10),
			lipgloss.Center, lipgloss.Center,
			m.dialog.View())
	} else if m.confirm != nil {
		content = lipgloss.Place(m.width, max(m.height-18, 10),
			lipgloss.Center, lipgloss.Center,
			m.confirm.View())
	} else if m.selectMenu != nil {
		content = lipgloss.Place(m.width, max(m.height-18, 10),
			lipgloss.Center, lipgloss.Center,
			m.selectMenu.View())
	} else if m.viewState == ViewHelp {
		content = m.renderHelpView()
	} else {
		switch m.activeTab {
		case TabDiscover:
			content = m.renderDiscoverView()
		case TabMounted:
			content = m.renderMountedView()
		case TabAutomount:
			content = m.renderAutomountView()
		case TabConfig:
			content = m.renderConfigView()
		}
	}

	statusBar := m.renderMainStatusBar()

	headerHeight := lipgloss.Height(header)
	tabsHeight := lipgloss.Height(tabs)
	statusHeight := lipgloss.Height(statusBar)
	contentHeight := max(m.height-headerHeight-tabsHeight-statusHeight-1, 1)

	sizedContent := lipgloss.NewStyle().
		Height(contentHeight).
		Width(m.width).
		Render(content)

	page := lipgloss.JoinVertical(lipgloss.Left,
		header,
		tabs,
		sizedContent,
		statusBar,
	)

	return page
}

func (m Model) renderDiscoverView() string {
	t := theme.Active
	contentHeight := max(m.height-18, 5)

	if m.viewState == ViewHostShares && m.selectedHost != "" {
		breadcrumb := components.RenderBreadcrumb([]string{"Discover", m.selectedHost}, t)

		shares := m.hostShares[m.selectedHost]
		if len(shares) == 0 {
			empty := lipgloss.NewStyle().
				Foreground(t.DarkForeground).
				Italic(true).
				Render("  No shares found on this host")
			return lipgloss.JoinVertical(lipgloss.Left,
				"  "+breadcrumb,
				"",
				empty,
			)
		}

		hint := lipgloss.NewStyle().
			Foreground(t.DarkForeground).
			Render("  enter:action  c:connectivity  esc:back")

		return lipgloss.JoinVertical(lipgloss.Left,
			"  "+breadcrumb,
			"",
			m.shareList.View(),
			hint,
		)
	}

	if m.scanning {
		spinnerView := m.spinner.View()
		scanMsg := lipgloss.NewStyle().Foreground(t.Accent).Render(" Scanning network...")
		scanBar := theme.GradientBar(min(m.width-8, 40), t)
		networkAnim := components.NetworkAnimation(m.frame, min(m.width-10, 50))

		return lipgloss.Place(m.width, contentHeight,
			lipgloss.Center, lipgloss.Center,
			lipgloss.JoinVertical(lipgloss.Center,
				networkAnim,
				"",
				spinnerView+scanMsg,
				"",
				scanBar,
			))
	}

	if len(m.hosts) == 0 {
		emptyStyle := lipgloss.NewStyle().
			Foreground(t.DarkForeground).
			Italic(true)

		accent := lipgloss.NewStyle().
			Foreground(t.Accent).
			Bold(true)

		networkAnim := components.NetworkAnimation(m.frame, min(m.width-10, 50))
		welcomeBox := components.BoxWithGlow(
			lipgloss.JoinVertical(lipgloss.Center,
				components.WaveText("Welcome to SMBark", m.frame, t),
				"",
				emptyStyle.Render("Press ")+accent.Render("s")+emptyStyle.Render(" to scan the network"),
				emptyStyle.Render("for SMB/CIFS shares"),
			),
			min(m.width-6, 50), m.frame, t)

		return lipgloss.Place(m.width, contentHeight,
			lipgloss.Center, lipgloss.Center,
			lipgloss.JoinVertical(lipgloss.Center,
				networkAnim,
				"",
				welcomeBox,
			))
	}

	hint := lipgloss.NewStyle().
		Foreground(t.DarkForeground).
		Render("  s:scan  a:add host  enter:browse  /:filter")

	return lipgloss.JoinVertical(lipgloss.Left,
		m.hostList.View(),
		hint,
	)
}

func (m Model) renderMountedView() string {
	t := theme.Active
	contentHeight := max(m.height-18, 5)

	if len(m.mountedShares) == 0 {
		emptyStyle := lipgloss.NewStyle().
			Foreground(t.DarkForeground).
			Italic(true)

		emptyBox := components.BoxWithGlow(
			lipgloss.JoinVertical(lipgloss.Center,
				components.WaveText("No SMB shares mounted", m.frame, t),
				"",
				emptyStyle.Render("Discover and mount shares"),
				emptyStyle.Render("from the Discover tab"),
			),
			min(m.width-6, 45), m.frame, t)

		return lipgloss.Place(m.width, contentHeight,
			lipgloss.Center, lipgloss.Center,
			emptyBox)
	}

	title := t.Gradient(fmt.Sprintf("  %d Mounted Share(s)", len(m.mountedShares)), t.Green, t.Cyan)
	hint := lipgloss.NewStyle().
		Foreground(t.DarkForeground).
		Render("  u:unmount  f:force unmount  r:refresh")

	return lipgloss.JoinVertical(lipgloss.Left,
		title,
		"",
		m.mountedList.View(),
		hint,
	)
}

func (m Model) renderAutomountView() string {
	t := theme.Active
	contentHeight := max(m.height-18, 5)

	if len(m.automounts) == 0 {
		emptyStyle := lipgloss.NewStyle().
			Foreground(t.DarkForeground).
			Italic(true)

		emptyBox := components.BoxWithGlow(
			lipgloss.JoinVertical(lipgloss.Center,
				components.WaveText("No automounts configured", m.frame, t),
				"",
				emptyStyle.Render("Set up automounts from"),
				emptyStyle.Render("the Discover tab"),
			),
			min(m.width-6, 45), m.frame, t)

		return lipgloss.Place(m.width, contentHeight,
			lipgloss.Center, lipgloss.Center,
			emptyBox)
	}

	title := t.Gradient(fmt.Sprintf("  %d Automount(s)", len(m.automounts)), t.Yellow, t.Orange)
	hint := lipgloss.NewStyle().
		Foreground(t.DarkForeground).
		Render("  d:remove  r:refresh")

	return lipgloss.JoinVertical(lipgloss.Left,
		title,
		"",
		m.automountList.View(),
		hint,
	)
}

func (m Model) renderConfigView() string {
	t := theme.Active

	titleStyle := lipgloss.NewStyle().Foreground(t.Accent).Bold(true)
	labelStyle := lipgloss.NewStyle().Foreground(t.LightForeground).Width(20)
	valueStyle := lipgloss.NewStyle().Foreground(t.Foreground)
	sectionWidth := min(m.width-6, 70)
	sectionStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(t.Selection).
		Padding(1, 2).
		Width(sectionWidth)

	mountedCount := len(m.mountedShares)
	automountCount := len(m.automounts)
	fstabCount := len(m.fstabEntries)

	mountedBar := components.MiniProgressBar(float64(mountedCount)/max(float64(mountedCount+automountCount), 1), 15, t)
	automountBar := components.MiniProgressBar(float64(automountCount)/max(float64(mountedCount+automountCount), 1), 15, t)

	overview := lipgloss.JoinVertical(lipgloss.Left,
		components.WaveText("System Overview", m.frame, t),
		"",
		labelStyle.Render("Mounted Shares:")+valueStyle.Render(fmt.Sprintf(" %d ", mountedCount))+mountedBar,
		labelStyle.Render("Automounts:")+valueStyle.Render(fmt.Sprintf(" %d ", automountCount))+automountBar,
		labelStyle.Render("Fstab Entries:")+valueStyle.Render(fmt.Sprintf("%d", fstabCount)),
		labelStyle.Render("Discovered Hosts:")+valueStyle.Render(fmt.Sprintf("%d", len(m.hosts))),
	)

	var fstabLines []string
	for _, s := range m.fstabEntries {
		line := fmt.Sprintf("  %s → %s", s.Path, s.MountPoint)
		fstabLines = append(fstabLines, lipgloss.NewStyle().Foreground(t.Foreground).Render(line))
	}
	if len(fstabLines) == 0 {
		fstabLines = append(fstabLines, lipgloss.NewStyle().Foreground(t.DarkForeground).Italic(true).Render("  No CIFS entries in /etc/fstab"))
	}

	_ = titleStyle
	fstab := lipgloss.JoinVertical(lipgloss.Left,
		append([]string{
			components.WaveText("CIFS Entries in /etc/fstab", m.frame+30, t),
			"",
		}, fstabLines...)...,
	)

	var connLines []string
	for host, cs := range m.connectivity {
		var line string
		if cs.Err != nil {
			line = fmt.Sprintf("  %s  %s  %s",
				lipgloss.NewStyle().Foreground(t.Red).Render("✗"),
				host,
				lipgloss.NewStyle().Foreground(t.Red).Render(cs.Err.Error()))
		} else {
			color := t.Green
			if cs.Latency > 100*time.Millisecond {
				color = t.Yellow
			}
			if cs.Latency > 500*time.Millisecond {
				color = t.Red
			}
			line = fmt.Sprintf("  %s  %s  %s",
				lipgloss.NewStyle().Foreground(t.Green).Render("✓"),
				host,
				lipgloss.NewStyle().Foreground(color).Render(fmt.Sprintf("%dms", cs.Latency.Milliseconds())))
		}
		connLines = append(connLines, line)
	}
	if len(connLines) == 0 {
		connLines = append(connLines, lipgloss.NewStyle().Foreground(t.DarkForeground).Italic(true).Render("  No connectivity checks performed"))
	}

	conn := lipgloss.JoinVertical(lipgloss.Left,
		append([]string{
			components.WaveText("Connectivity Status", m.frame+60, t),
			"",
		}, connLines...)...,
	)

	content := lipgloss.JoinVertical(lipgloss.Left,
		sectionStyle.Render(overview),
		"",
		sectionStyle.Render(fstab),
		"",
		sectionStyle.Render(conn),
	)

	hint := lipgloss.NewStyle().
		Foreground(t.DarkForeground).
		Render("  r:refresh all")

	return lipgloss.JoinVertical(lipgloss.Left,
		"",
		content,
		"",
		hint,
	)
}

func (m Model) renderHelpView() string {
	t := theme.Active

	title := theme.SparkleText("Help & Keybindings", t)

	content := lipgloss.JoinVertical(lipgloss.Left,
		"",
		lipgloss.NewStyle().Padding(0, 2).Render(title),
		"",
		m.helpViewport.View(),
		"",
		lipgloss.NewStyle().Foreground(t.DarkForeground).Render("  ?:close help  ↑↓:scroll"),
	)
	return content
}

func renderHelpContent(t *theme.Theme) string {
	h := lipgloss.NewStyle().Foreground(t.Accent).Bold(true)
	k := lipgloss.NewStyle().Foreground(t.Yellow).Bold(true)
	d := lipgloss.NewStyle().Foreground(t.LightForeground)

	sections := []struct {
		title string
		keys  []struct{ key, desc string }
	}{
		{
			title: "Global",
			keys: []struct{ key, desc string }{
				{"tab/shift+tab", "Switch tabs"},
				{"?", "Toggle help"},
				{"q/ctrl+c", "Quit (from main view)"},
				{"esc", "Go back"},
			},
		},
		{
			title: "Discover Tab",
			keys: []struct{ key, desc string }{
				{"s/r", "Scan network for SMB hosts"},
				{"a", "Add host manually by IP/hostname"},
				{"enter", "Browse host shares / action menu"},
				{"/", "Filter hosts"},
				{"c", "Check host connectivity"},
			},
		},
		{
			title: "Mounted Tab",
			keys: []struct{ key, desc string }{
				{"u", "Unmount selected share"},
				{"f", "Force unmount (lazy)"},
				{"r", "Refresh mount list"},
			},
		},
		{
			title: "Automount Tab",
			keys: []struct{ key, desc string }{
				{"d/del", "Remove automount"},
				{"r", "Refresh automount list"},
			},
		},
	}

	var lines []string
	for _, sec := range sections {
		lines = append(lines, h.Render("  "+sec.title))
		lines = append(lines, "")
		for _, binding := range sec.keys {
			lines = append(lines, fmt.Sprintf("    %s  %s",
				k.Render(fmt.Sprintf("%-16s", binding.key)),
				d.Render(binding.desc)))
		}
		lines = append(lines, "")
	}

	lines = append(lines, h.Render("  About"))
	lines = append(lines, "")
	lines = append(lines, d.Render("  SMBark — A beautiful TUI for managing SMB shares"))
	lines = append(lines, d.Render("  Built with Charm libraries (Bubble Tea, Lip Gloss, Bubbles)"))
	lines = append(lines, d.Render("  Omarchy theme support included"))

	return strings.Join(lines, "\n")
}

func (m Model) renderMainStatusBar() string {
	t := theme.Active
	tabName := Tabs[m.activeTab].Icon + " " + Tabs[m.activeTab].Name

	mountedCount := len(m.mountedShares)

	statusLine := components.RenderStatusBar(m.width, tabName, mountedCount, t)

	if m.statusMsg != "" {
		msgStyle := lipgloss.NewStyle().Foreground(t.Green).Padding(0, 1)
		if m.statusErr {
			msgStyle = msgStyle.Foreground(t.Red)
		}

		icon := "✓"
		if m.statusErr {
			icon = "✗"
		}
		if m.scanning || m.mounting {
			icon = m.spinner.View()
		}

		msgLine := lipgloss.NewStyle().
			Background(t.DarkBackground).
			Width(m.width).
			Render(msgStyle.Render(icon + " " + m.statusMsg))

		return lipgloss.JoinVertical(lipgloss.Left, msgLine, statusLine)
	}

	return statusLine
}

func isSudoError(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "sudo requires a password") ||
		strings.Contains(s, "password is required")
}

func (m *Model) handleSudoError(err error, retryCmd tea.Cmd) bool {
	if !isSudoError(err) {
		return false
	}
	m.pendingRetry = retryCmd
	return true
}

func (m *Model) promptSudo() tea.Cmd {
	m.setStatus("Authentication required — entering sudo prompt...", false)
	c := exec.Command("sudo", "-v")
	return tea.ExecProcess(c, func(err error) tea.Msg {
		return SudoRefreshedMsg{Err: err}
	})
}
