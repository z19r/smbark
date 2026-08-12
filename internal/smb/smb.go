package smb

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

type ShareType string

const (
	ShareTypeDisk    ShareType = "Disk"
	ShareTypePrinter ShareType = "Printer"
	ShareTypeIPC     ShareType = "IPC"
)

type Host struct {
	Name      string
	IP        string
	Workgroup string
}

type Share struct {
	Host        string
	Name        string
	Type        ShareType
	Comment     string
	Path        string // UNC path: //host/share
	MountPoint  string
	IsMounted   bool
	IsAutomount bool
	SizeTotal   uint64
	SizeUsed    uint64
	SizeFree    uint64
}

type Credentials struct {
	Username string
	Password string
	Domain   string
}

type MountOptions struct {
	Version    string // SMB protocol version
	Security   string // Security mode (krb5, ntlm, etc.)
	UID        string
	GID        string
	FileMode   string
	DirMode    string
	ReadOnly   bool
	MountPoint string
	Creds      Credentials
}

func DefaultMountOptions() MountOptions {
	return MountOptions{
		Version:  "3.0",
		Security: "ntlmssp",
		UID:      fmt.Sprintf("%d", os.Getuid()),
		GID:      fmt.Sprintf("%d", os.Getgid()),
		FileMode: "0755",
		DirMode:  "0755",
	}
}

func DiscoverHosts(ctx context.Context) ([]Host, error) {
	var hosts []Host
	seen := make(map[string]bool)

	if h, err := discoverAvahi(ctx); err == nil {
		for _, host := range h {
			key := host.IP
			if !seen[key] {
				seen[key] = true
				hosts = append(hosts, host)
			}
		}
	}

	if h, err := discoverNMB(ctx); err == nil {
		for _, host := range h {
			key := host.IP
			if key == "" {
				key = host.Name
			}
			if !seen[key] {
				seen[key] = true
				hosts = append(hosts, host)
			}
		}
	}

	if h, err := discoverSubnetScan(ctx); err == nil {
		for _, host := range h {
			if !seen[host.IP] {
				seen[host.IP] = true
				hosts = append(hosts, host)
			}
		}
	}

	return hosts, nil
}

func getLocalIP() (net.IP, error) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil, err
	}
	for _, addr := range addrs {
		ipNet, ok := addr.(*net.IPNet)
		if !ok || ipNet.IP.IsLoopback() || ipNet.IP.To4() == nil {
			continue
		}
		return ipNet.IP.To4(), nil
	}
	return nil, fmt.Errorf("no suitable network interface")
}

func discoverSubnetScan(ctx context.Context) ([]Host, error) {
	localIP, err := getLocalIP()
	if err != nil {
		return nil, err
	}

	// Scan the /24 around the host's actual IP
	baseIP := make(net.IP, 4)
	copy(baseIP, localIP)
	baseIP[3] = 0
	hostCount := 254

	var mu sync.Mutex
	var hosts []Host
	var wg sync.WaitGroup
	sem := make(chan struct{}, 50) // limit concurrency

	for i := 1; i <= hostCount; i++ {
		ip := make(net.IP, 4)
		copy(ip, baseIP)
		ip[3] = byte(i)
		target := ip.String()

		wg.Add(1)
		go func(addr string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			conn, err := net.DialTimeout("tcp", addr+":445", 500*time.Millisecond)
			if err != nil {
				return
			}
			conn.Close()

			name := addr
			if names, err := net.LookupAddr(addr); err == nil && len(names) > 0 {
				name = strings.TrimSuffix(names[0], ".")
			}

			mu.Lock()
			hosts = append(hosts, Host{Name: name, IP: addr})
			mu.Unlock()
		}(target)
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-ctx.Done():
	}

	return hosts, nil
}

func discoverAvahi(ctx context.Context) ([]Host, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "avahi-browse", "-t", "-r", "-p", "_smb._tcp")
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	var hosts []Host
	seen := make(map[string]bool)
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Split(line, ";")
		if len(fields) < 8 || fields[0] != "=" {
			continue
		}
		name := fields[3]
		ip := fields[7]
		if ip != "" && !seen[ip] {
			seen[ip] = true
			hosts = append(hosts, Host{Name: name, IP: ip})
		}
	}
	return hosts, nil
}

func discoverNMB(ctx context.Context) ([]Host, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "nmblookup", "-S", "__SAMBA__")
	out, _ := cmd.Output()

	cmd2 := exec.CommandContext(ctx, "nmblookup", "-S", "*")
	out2, _ := cmd2.Output()

	combined := string(out) + "\n" + string(out2)

	var hosts []Host
	seen := make(map[string]bool)
	re := regexp.MustCompile(`(\d+\.\d+\.\d+\.\d+)\s+(\S+)`)
	for _, match := range re.FindAllStringSubmatch(combined, -1) {
		ip := match[1]
		name := match[2]
		if !seen[ip] {
			seen[ip] = true
			hosts = append(hosts, Host{Name: name, IP: ip})
		}
	}
	return hosts, nil
}

func ListShares(ctx context.Context, host string, creds Credentials) ([]Share, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	args := []string{"-L", host, "-N", "--no-pass"}
	if creds.Username != "" {
		args = []string{"-L", host, "-U", creds.Username}
		if creds.Domain != "" {
			args = append(args, "-W", creds.Domain)
		}
	}

	cmd := exec.CommandContext(ctx, "smbclient", args...)
	if creds.Password != "" {
		cmd.Stdin = strings.NewReader(creds.Password + "\n")
	}
	out, err := cmd.CombinedOutput()
	if err != nil && len(out) == 0 {
		return nil, fmt.Errorf("smbclient failed: %w", err)
	}

	return parseSmbclientList(host, string(out)), nil
}

func parseSmbclientList(host, output string) []Share {
	var shares []Share
	lines := strings.Split(output, "\n")
	inShareList := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "Sharename") {
			inShareList = true
			continue
		}
		if strings.HasPrefix(trimmed, "---------") || strings.HasPrefix(trimmed, "=========") {
			continue
		}
		if trimmed == "" {
			if inShareList {
				inShareList = false
			}
			continue
		}
		if !inShareList {
			continue
		}

		parts := strings.Fields(trimmed)
		if len(parts) < 2 {
			continue
		}

		name := parts[0]
		typ := parts[1]
		comment := ""
		if len(parts) > 2 {
			comment = strings.Join(parts[2:], " ")
		}

		var shareType ShareType
		switch strings.ToLower(typ) {
		case "disk":
			shareType = ShareTypeDisk
		case "printer":
			shareType = ShareTypePrinter
		case "ipc":
			shareType = ShareTypeIPC
		default:
			shareType = ShareTypeDisk
		}

		shares = append(shares, Share{
			Host:    host,
			Name:    name,
			Type:    shareType,
			Comment: comment,
			Path:    fmt.Sprintf("//%s/%s", host, name),
		})
	}
	return shares
}

func GetMountedShares() ([]Share, error) {
	data, err := os.ReadFile("/proc/mounts")
	if err != nil {
		return nil, err
	}

	var shares []Share
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		if fields[2] != "cifs" && fields[2] != "smb3" {
			continue
		}

		path := fields[0]
		mountPoint := fields[1]

		pathParts := strings.SplitN(strings.TrimPrefix(path, "//"), "/", 2)
		host := ""
		name := ""
		if len(pathParts) >= 1 {
			host = pathParts[0]
		}
		if len(pathParts) >= 2 {
			name = pathParts[1]
		}

		share := Share{
			Host:       host,
			Name:       name,
			Type:       ShareTypeDisk,
			Path:       path,
			MountPoint: mountPoint,
			IsMounted:  true,
		}
		fillDiskUsage(&share)
		shares = append(shares, share)
	}
	return shares, nil
}

func fillDiskUsage(s *Share) {
	if s.MountPoint == "" {
		return
	}
	cmd := exec.Command("df", "--output=size,used,avail", "-B1", s.MountPoint)
	out, err := cmd.Output()
	if err != nil {
		return
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) < 2 {
		return
	}
	fields := strings.Fields(lines[1])
	if len(fields) >= 3 {
		fmt.Sscanf(fields[0], "%d", &s.SizeTotal)
		fmt.Sscanf(fields[1], "%d", &s.SizeUsed)
		fmt.Sscanf(fields[2], "%d", &s.SizeFree)
	}
}

func MountShare(share Share, opts MountOptions) error {
	mountPoint := opts.MountPoint
	if mountPoint == "" {
		mountPoint = filepath.Join("/mnt/smb", share.Host, share.Name)
	}

	// Clean up any stale/failed mount before attempting
	sudoRun("umount", "-l", mountPoint)
	if err := sudoRun("mkdir", "-p", mountPoint); err != nil {
		return fmt.Errorf("failed to create mount point: %w", err)
	}

	isGuest := opts.Creds.Username == ""

	var mountOpts []string
	if opts.Version != "" {
		mountOpts = append(mountOpts, "vers="+opts.Version)
	}
	if isGuest {
		mountOpts = append(mountOpts, "guest", "sec=none")
	} else {
		if opts.Security != "" {
			mountOpts = append(mountOpts, "sec="+opts.Security)
		}
		mountOpts = append(mountOpts, "username="+opts.Creds.Username)
		if opts.Creds.Domain != "" {
			mountOpts = append(mountOpts, "domain="+opts.Creds.Domain)
		}
	}
	if opts.UID != "" {
		mountOpts = append(mountOpts, "uid="+opts.UID)
	}
	if opts.GID != "" {
		mountOpts = append(mountOpts, "gid="+opts.GID)
	}
	if opts.FileMode != "" {
		mountOpts = append(mountOpts, "file_mode="+opts.FileMode)
	}
	if opts.DirMode != "" {
		mountOpts = append(mountOpts, "dir_mode="+opts.DirMode)
	}
	if opts.ReadOnly {
		mountOpts = append(mountOpts, "ro")
	}

	args := []string{"-t", "cifs", share.Path, mountPoint}
	if len(mountOpts) > 0 {
		args = append(args, "-o", strings.Join(mountOpts, ","))
	}

	cmd := exec.Command("sudo", append([]string{"-n", "mount"}, args...)...)
	if opts.Creds.Password != "" {
		cmd.Env = append(os.Environ(), "PASSWD="+opts.Creds.Password)
	}

	if out, err := cmd.CombinedOutput(); err != nil {
		outStr := strings.TrimSpace(string(out))
		if strings.Contains(outStr, "password is required") || strings.Contains(outStr, "a password is required") {
			return fmt.Errorf("sudo requires a password — run 'sudo -v' in another terminal first")
		}
		return fmt.Errorf("mount failed: %s", outStr)
	}
	return nil
}

func UnmountShare(mountPoint string) error {
	return sudoRun("umount", mountPoint)
}

func ForceUnmountShare(mountPoint string) error {
	return sudoRun("umount", "-l", mountPoint)
}

type AutomountConfig struct {
	Share      Share
	MountPoint string
	Options    MountOptions
}

func CreateAutomount(cfg AutomountConfig) error {
	mountPoint := cfg.MountPoint
	if mountPoint == "" {
		mountPoint = filepath.Join("/mnt/smb", cfg.Share.Host, cfg.Share.Name)
	}

	unitName, err := systemdEscapePath(mountPoint)
	if err != nil {
		unitName = systemdUnitName(mountPoint)
	}

	// Stop any existing broken automount/mount for this path
	sudoRun("systemctl", "stop", unitName+".automount")
	sudoRun("systemctl", "stop", unitName+".mount")
	sudoRun("umount", "-l", mountPoint)

	if err := sudoRun("mkdir", "-p", mountPoint); err != nil {
		return fmt.Errorf("failed to create mount point %s: %w", mountPoint, err)
	}

	credFile := ""
	if cfg.Options.Creds.Username != "" {
		credDir := "/etc/samba/credentials"
		if err := sudoRun("mkdir", "-p", credDir); err != nil {
			return fmt.Errorf("failed to create credentials dir: %w", err)
		}

		credFile = filepath.Join(credDir, unitName)
		credContent := fmt.Sprintf("username=%s\npassword=%s\n", cfg.Options.Creds.Username, cfg.Options.Creds.Password)
		if cfg.Options.Creds.Domain != "" {
			credContent += fmt.Sprintf("domain=%s\n", cfg.Options.Creds.Domain)
		}
		if err := sudoWrite(credFile, credContent); err != nil {
			return fmt.Errorf("failed to write credentials: %w", err)
		}
		sudoRun("chmod", "600", credFile)
	}

	isGuest := credFile == ""

	var mountOpts []string
	if cfg.Options.Version != "" {
		mountOpts = append(mountOpts, "vers="+cfg.Options.Version)
	}
	if isGuest {
		mountOpts = append(mountOpts, "guest", "sec=none")
	} else {
		if cfg.Options.Security != "" {
			mountOpts = append(mountOpts, "sec="+cfg.Options.Security)
		}
		mountOpts = append(mountOpts, "credentials="+credFile)
	}
	mountOpts = append(mountOpts, fmt.Sprintf("uid=%s", cfg.Options.UID))
	mountOpts = append(mountOpts, fmt.Sprintf("gid=%s", cfg.Options.GID))
	mountOpts = append(mountOpts, fmt.Sprintf("file_mode=%s", cfg.Options.FileMode))
	mountOpts = append(mountOpts, fmt.Sprintf("dir_mode=%s", cfg.Options.DirMode))
	mountOpts = append(mountOpts, "_netdev")

	mountUnit := fmt.Sprintf(`[Unit]
Description=SMB mount for %s
After=network-online.target
Wants=network-online.target

[Mount]
What=%s
Where=%s
Type=cifs
Options=%s

[Install]
WantedBy=multi-user.target
`, cfg.Share.Path, cfg.Share.Path, mountPoint, strings.Join(mountOpts, ","))

	automountUnit := fmt.Sprintf(`[Unit]
Description=SMB automount for %s
After=network-online.target
Wants=network-online.target

[Automount]
Where=%s
TimeoutIdleSec=300

[Install]
WantedBy=multi-user.target
`, cfg.Share.Path, mountPoint)

	mountPath := filepath.Join("/etc/systemd/system", unitName+".mount")
	automountPath := filepath.Join("/etc/systemd/system", unitName+".automount")

	if err := sudoWrite(mountPath, mountUnit); err != nil {
		return fmt.Errorf("failed to write mount unit: %w", err)
	}
	if err := sudoWrite(automountPath, automountUnit); err != nil {
		return fmt.Errorf("failed to write automount unit: %w", err)
	}

	if err := sudoRun("systemctl", "daemon-reload"); err != nil {
		return fmt.Errorf("daemon-reload failed: %w", err)
	}
	if err := sudoRun("systemctl", "enable", unitName+".automount"); err != nil {
		return fmt.Errorf("enable failed: %w", err)
	}
	if err := sudoRun("systemctl", "start", unitName+".automount"); err != nil {
		return fmt.Errorf("start failed: %w", err)
	}

	// Trigger the mount immediately so it shows up in /proc/mounts
	sudoRun("systemctl", "start", unitName+".mount")

	return nil
}

func sudoRun(args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sudo", append([]string{"-n"}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		outStr := strings.TrimSpace(string(out))
		if strings.Contains(outStr, "password is required") || strings.Contains(outStr, "a password is required") {
			return fmt.Errorf("sudo requires a password — run 'sudo -v' in another terminal first")
		}
		return fmt.Errorf("%s: %s", args[0], outStr)
	}
	return nil
}

func sudoWrite(path, content string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sudo", "-n", "tee", path)
	cmd.Stdin = strings.NewReader(content)
	var stderr strings.Builder
	cmd.Stdout = nil
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		errStr := strings.TrimSpace(stderr.String())
		if strings.Contains(errStr, "password is required") || strings.Contains(errStr, "a password is required") {
			return fmt.Errorf("sudo requires a password — run 'sudo -v' in another terminal first")
		}
		return fmt.Errorf("write %s: %s", path, errStr)
	}
	return nil
}

func systemdEscapePath(path string) (string, error) {
	cmd := exec.Command("systemd-escape", "-p", path)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func RemoveAutomount(mountPoint string) error {
	unitName, err := systemdEscapePath(mountPoint)
	if err != nil {
		unitName = systemdUnitName(mountPoint)
	}

	if err := sudoRun("systemctl", "stop", unitName+".automount"); err != nil {
		return fmt.Errorf("stop automount: %w", err)
	}
	sudoRun("systemctl", "disable", unitName+".automount")
	sudoRun("systemctl", "stop", unitName+".mount")

	if err := sudoRun("rm", "-f",
		filepath.Join("/etc/systemd/system", unitName+".mount"),
		filepath.Join("/etc/systemd/system", unitName+".automount"),
	); err != nil {
		return fmt.Errorf("remove unit files: %w", err)
	}

	sudoRun("systemctl", "daemon-reload")
	return nil
}

func GetAutomounts() ([]AutomountConfig, error) {
	cmd := exec.Command("systemctl", "list-units", "--type=automount", "--all", "--no-legend", "--no-pager")
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	var configs []AutomountConfig
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 1 {
			continue
		}
		unit := fields[0]
		if !strings.HasSuffix(unit, ".automount") {
			continue
		}

		mountUnit := strings.TrimSuffix(unit, ".automount") + ".mount"
		showCmd := exec.Command("systemctl", "show", mountUnit, "--property=What,Where,Options")
		showOut, err := showCmd.Output()
		if err != nil {
			continue
		}

		props := make(map[string]string)
		for _, line := range strings.Split(string(showOut), "\n") {
			parts := strings.SplitN(line, "=", 2)
			if len(parts) == 2 {
				props[parts[0]] = parts[1]
			}
		}

		what := props["What"]
		if !strings.HasPrefix(what, "//") {
			continue
		}

		pathParts := strings.SplitN(strings.TrimPrefix(what, "//"), "/", 2)
		host := ""
		name := ""
		if len(pathParts) >= 1 {
			host = pathParts[0]
		}
		if len(pathParts) >= 2 {
			name = pathParts[1]
		}

		configs = append(configs, AutomountConfig{
			Share: Share{
				Host:       host,
				Name:       name,
				Path:       what,
				MountPoint: props["Where"],
			},
			MountPoint: props["Where"],
		})
	}
	return configs, nil
}

func systemdUnitName(mountPoint string) string {
	mp := strings.TrimPrefix(mountPoint, "/")
	mp = strings.ReplaceAll(mp, "/", "-")
	return mp
}

func CheckConnectivity(host string) (time.Duration, error) {
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "smbclient", "-L", host, "-N", "--no-pass", "-m", "SMB3")
	err := cmd.Run()
	elapsed := time.Since(start)
	if err != nil {
		cmd2 := exec.CommandContext(ctx, "ping", "-c", "1", "-W", "2", host)
		if err2 := cmd2.Run(); err2 != nil {
			return elapsed, fmt.Errorf("host unreachable")
		}
		return elapsed, fmt.Errorf("SMB service unavailable")
	}
	return elapsed, nil
}

func GetFstabEntries() ([]Share, error) {
	data, err := os.ReadFile("/etc/fstab")
	if err != nil {
		return nil, err
	}

	var shares []Share
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "#") || line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		if fields[2] != "cifs" && fields[2] != "smb3" {
			continue
		}

		path := fields[0]
		mountPoint := fields[1]
		pathParts := strings.SplitN(strings.TrimPrefix(path, "//"), "/", 2)
		host := ""
		name := ""
		if len(pathParts) >= 1 {
			host = pathParts[0]
		}
		if len(pathParts) >= 2 {
			name = pathParts[1]
		}

		shares = append(shares, Share{
			Host:       host,
			Name:       name,
			Path:       path,
			MountPoint: mountPoint,
			Type:       ShareTypeDisk,
		})
	}
	return shares, nil
}

func AddFstabEntry(share Share, opts MountOptions) error {
	mountPoint := opts.MountPoint
	if mountPoint == "" {
		mountPoint = filepath.Join("/mnt/smb", share.Host, share.Name)
	}

	var mountOpts []string
	if opts.Creds.Username != "" {
		credFile := fmt.Sprintf("/etc/samba/credentials/%s-%s", share.Host, share.Name)
		mountOpts = append(mountOpts, "credentials="+credFile)
	} else {
		mountOpts = append(mountOpts, "guest")
	}
	if opts.Version != "" {
		mountOpts = append(mountOpts, "vers="+opts.Version)
	}
	mountOpts = append(mountOpts, "_netdev", "nofail")
	if opts.UID != "" {
		mountOpts = append(mountOpts, "uid="+opts.UID)
	}
	if opts.GID != "" {
		mountOpts = append(mountOpts, "gid="+opts.GID)
	}

	entry := fmt.Sprintf("%s\t%s\tcifs\t%s\t0\t0\n",
		share.Path, mountPoint, strings.Join(mountOpts, ","))

	cmd := exec.Command("sudo", "bash", "-c", fmt.Sprintf("echo %q >> /etc/fstab", entry))
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to add fstab entry: %s: %w", string(out), err)
	}
	return nil
}

func FormatSize(bytes uint64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
		TB = GB * 1024
	)
	switch {
	case bytes >= TB:
		return fmt.Sprintf("%.1f TB", float64(bytes)/float64(TB))
	case bytes >= GB:
		return fmt.Sprintf("%.1f GB", float64(bytes)/float64(GB))
	case bytes >= MB:
		return fmt.Sprintf("%.1f MB", float64(bytes)/float64(MB))
	case bytes >= KB:
		return fmt.Sprintf("%.1f KB", float64(bytes)/float64(KB))
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}
