package ui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/z19r/smbark/internal/smb"
)

type TickMsg time.Time

type HostsDiscoveredMsg struct {
	Hosts []smb.Host
	Err   error
}

type SharesListedMsg struct {
	Host   string
	Shares []smb.Share
	Err    error
}

type MountedSharesMsg struct {
	Shares []smb.Share
	Err    error
}

type AutomountsMsg struct {
	Configs []smb.AutomountConfig
	Err     error
}

type FstabEntriesMsg struct {
	Shares []smb.Share
	Err    error
}

type MountResultMsg struct {
	Share smb.Share
	Err   error
}

type UnmountResultMsg struct {
	MountPoint string
	Err        error
}

type AutomountCreateMsg struct {
	Share smb.Share
	Err   error
}

type AutomountRemoveMsg struct {
	MountPoint string
	Err        error
}

type ConnectivityMsg struct {
	Host    string
	Latency time.Duration
	Err     error
}

type StatusMsg struct {
	Message string
	IsError bool
}

type SudoRefreshedMsg struct {
	Err error
}

type SudoNeededMsg struct {
	RetryCmd tea.Cmd
}
