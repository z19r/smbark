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

// AutoVersion is the sentinel MountOptions.Version value meaning "probe the
// server for the best dialect it supports" rather than pinning one.
const AutoVersion = "auto"

// autoDialects lists the SMB dialects to probe, most secure first, down to the
// lowest we select automatically. SMB1 ("1.0") is deliberately excluded: it is
// unencrypted and deprecated, so it is only used when set explicitly. Modern
// kernels refuse anything below 2.1 unless it is named, which is why 2.0 must
// be listed here for older NAS boxes that top out at SMB 2.0.
var autoDialects = []string{"3.1.1", "3.0", "2.1", "2.0"}

// versionsToTry expands a configured version into the ordered list of dialects
// to attempt. An explicit version is honored as-is; "" or AutoVersion expands
// to the probe list.
func versionsToTry(version string) []string {
	if version == "" || version == AutoVersion {
		return autoDialects
	}
	return []string{version}
}

// isDialectError reports whether a mount failure looks like a protocol/dialect
// negotiation rejection (EINVAL / "error(22)") that is worth retrying at a
// lower dialect, versus a credential or connectivity failure we surface as-is.
func isDialectError(output string) bool {
	return strings.Contains(output, "error(22)") ||
		strings.Contains(output, "Invalid argument")
}

func DefaultMountOptions() MountOptions {
	return MountOptions{
		Version:  AutoVersion,
		Security: "ntlmssp",
		UID:      fmt.Sprintf("%d", os.Getuid()),
		GID:      fmt.Sprintf("%d", os.Getgid()),
		FileMode: "0755",
		DirMode:  "0755",
	}
}

// sysExec is the single seam through which every external command runs. Tests
// replace it to simulate command output/exit codes and to record the exact
// command sequence the mount/automount orchestration issues. env and stdin are
// optional; the returned string is the command's combined stdout+stderr.
var sysExec = func(ctx context.Context, env []string, stdin, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// attemptCifsMount runs a single `mount -t cifs` with the given comma-joined
// -o option string, returning the trimmed combined output. password, if
// non-empty, is passed via the PASSWD env var (the inline-credentials path).
func attemptCifsMount(sharePath, mountPoint, optString, password string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var env []string
	if password != "" {
		env = []string{"PASSWD=" + password}
	}
	out, err := sysExec(ctx, env, "", "sudo", "-n", "mount", "-t", "cifs", sharePath, mountPoint, "-o", optString)
	return strings.TrimSpace(out), err
}

// mountWithNegotiation tries each candidate dialect in order, leaving the share
// mounted on the first success and returning the version that worked. It only
// advances to a lower dialect on a negotiation-style rejection; a credential or
// connectivity error is returned immediately so it is not masked.
func mountWithNegotiation(sharePath, mountPoint string, baseOpts, versions []string, password string) (string, error) {
	var lastErr error
	for _, v := range versions {
		optString := strings.Join(append([]string{"vers=" + v}, baseOpts...), ",")
		out, err := attemptCifsMount(sharePath, mountPoint, optString, password)
		if err == nil {
			return v, nil
		}
		if strings.Contains(out, "password is required") {
			return "", fmt.Errorf("sudo requires a password — run 'sudo -v' in another terminal first")
		}
		lastErr = fmt.Errorf("mount failed (vers=%s): %s", v, out)
		if !isDialectError(out) {
			return "", lastErr
		}
	}
	return "", lastErr
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

// splitUNCPath splits a CIFS UNC path ("//host/share") into its host and share
// name components. Either may be empty if the path is malformed.
func splitUNCPath(path string) (host, name string) {
	parts := strings.SplitN(strings.TrimPrefix(path, "//"), "/", 2)
	if len(parts) >= 1 {
		host = parts[0]
	}
	if len(parts) >= 2 {
		name = parts[1]
	}
	return host, name
}

func discoverAvahi(ctx context.Context) ([]Host, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	out, err := sysExec(ctx, nil, "", "avahi-browse", "-t", "-r", "-p", "_smb._tcp")
	if err != nil {
		return nil, err
	}
	return parseAvahi(out), nil
}

// parseAvahi extracts hosts from `avahi-browse -p` machine-readable output.
func parseAvahi(output string) []Host {
	var hosts []Host
	seen := make(map[string]bool)
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		fields := strings.Split(scanner.Text(), ";")
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
	return hosts
}

func discoverNMB(ctx context.Context) ([]Host, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	out, _ := sysExec(ctx, nil, "", "nmblookup", "-S", "__SAMBA__")
	out2, _ := sysExec(ctx, nil, "", "nmblookup", "-S", "*")
	return parseNMB(out + "\n" + out2), nil
}

var nmbLineRe = regexp.MustCompile(`(\d+\.\d+\.\d+\.\d+)\s+(\S+)`)

// parseNMB extracts hosts from combined `nmblookup -S` output.
func parseNMB(combined string) []Host {
	var hosts []Host
	seen := make(map[string]bool)
	for _, match := range nmbLineRe.FindAllStringSubmatch(combined, -1) {
		ip := match[1]
		name := match[2]
		if !seen[ip] {
			seen[ip] = true
			hosts = append(hosts, Host{Name: name, IP: ip})
		}
	}
	return hosts
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

	stdin := ""
	if creds.Password != "" {
		stdin = creds.Password + "\n"
	}
	out, err := sysExec(ctx, nil, stdin, "smbclient", args...)
	if err != nil && len(out) == 0 {
		return nil, fmt.Errorf("smbclient failed: %w", err)
	}

	return parseSmbclientList(host, out), nil
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

	shares := parseCifsMounts(string(data))
	for i := range shares {
		fillDiskUsage(&shares[i])
	}
	return shares, nil
}

// parseCifsMounts extracts cifs/smb3 mounts from /proc/mounts-style content.
func parseCifsMounts(data string) []Share {
	var shares []Share
	scanner := bufio.NewScanner(strings.NewReader(data))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 3 {
			continue
		}
		if fields[2] != "cifs" && fields[2] != "smb3" {
			continue
		}

		path := fields[0]
		host, name := splitUNCPath(path)
		shares = append(shares, Share{
			Host:       host,
			Name:       name,
			Type:       ShareTypeDisk,
			Path:       path,
			MountPoint: fields[1],
			IsMounted:  true,
		})
	}
	return shares
}

func fillDiskUsage(s *Share) {
	if s.MountPoint == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := sysExec(ctx, nil, "", "df", "--output=size,used,avail", "-B1", s.MountPoint)
	if err != nil {
		return
	}
	total, used, free, ok := parseDiskUsage(out)
	if ok {
		s.SizeTotal, s.SizeUsed, s.SizeFree = total, used, free
	}
}

// parseDiskUsage parses `df --output=size,used,avail -B1` output (a header line
// followed by one data line of three byte counts).
func parseDiskUsage(output string) (total, used, free uint64, ok bool) {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) < 2 {
		return 0, 0, 0, false
	}
	fields := strings.Fields(lines[1])
	if len(fields) < 3 {
		return 0, 0, 0, false
	}
	fmt.Sscanf(fields[0], "%d", &total)
	fmt.Sscanf(fields[1], "%d", &used)
	fmt.Sscanf(fields[2], "%d", &free)
	return total, used, free, true
}

// unitNameForPath returns the systemd unit basename for a mount point, using
// systemd-escape when available and falling back to a manual escape.
func unitNameForPath(mountPoint string) string {
	if unit, err := systemdEscapePath(mountPoint); err == nil {
		return unit
	}
	return systemdUnitName(mountPoint)
}

// clearAutomountAt tears down any systemd automount/mount currently occupying
// mountPoint so a fresh `mount -t cifs` can bind the path directly. An active
// .automount turns the directory into an autofs point; mount.cifs canonicalises
// its target, which triggers the autofs and fails the mount with ENODEV
// ("No such device") before any dialect is even attempted — which in turn
// aborts dialect negotiation. Stopping the units removes the autofs trigger.
func clearAutomountAt(mountPoint string) {
	unitName := unitNameForPath(mountPoint)
	sudoRun("systemctl", "stop", unitName+".automount")
	sudoRun("systemctl", "stop", unitName+".mount")
	sudoRun("umount", "-l", mountPoint)
}

func MountShare(share Share, opts MountOptions) error {
	mountPoint := opts.MountPoint
	if mountPoint == "" {
		mountPoint = filepath.Join("/mnt/smb", share.Host, share.Name)
	}

	// Tear down any automount (or stale mount) occupying the path first. Mounting
	// onto an active .automount point fails with ENODEV before any dialect is
	// tried, which also aborts dialect negotiation — see clearAutomountAt.
	clearAutomountAt(mountPoint)
	if err := sudoRun("mkdir", "-p", mountPoint); err != nil {
		return fmt.Errorf("failed to create mount point: %w", err)
	}

	isGuest := opts.Creds.Username == ""

	// baseOpts holds every option except vers=, which mountWithNegotiation
	// prepends per candidate dialect.
	var baseOpts []string
	if isGuest {
		baseOpts = append(baseOpts, "guest", "sec=none")
	} else {
		if opts.Security != "" {
			baseOpts = append(baseOpts, "sec="+opts.Security)
		}
		baseOpts = append(baseOpts, "username="+opts.Creds.Username)
		if opts.Creds.Domain != "" {
			baseOpts = append(baseOpts, "domain="+opts.Creds.Domain)
		}
	}
	if opts.UID != "" {
		baseOpts = append(baseOpts, "uid="+opts.UID)
	}
	if opts.GID != "" {
		baseOpts = append(baseOpts, "gid="+opts.GID)
	}
	if opts.FileMode != "" {
		baseOpts = append(baseOpts, "file_mode="+opts.FileMode)
	}
	if opts.DirMode != "" {
		baseOpts = append(baseOpts, "dir_mode="+opts.DirMode)
	}
	if opts.ReadOnly {
		baseOpts = append(baseOpts, "ro")
	}

	_, err := mountWithNegotiation(share.Path, mountPoint, baseOpts, versionsToTry(opts.Version), opts.Creds.Password)
	return err
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

	unitName := unitNameForPath(mountPoint)

	// Stop any existing broken automount/mount for this path so the probe mount
	// below can bind the path directly instead of tripping the autofs point.
	clearAutomountAt(mountPoint)

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

	// baseOpts excludes vers=; the concrete dialect is resolved by probing so
	// the persisted unit pins a version the server actually supports (a unit
	// hard-coded to vers=3.0 silently fails at boot on older SMB2.0 servers).
	var baseOpts []string
	if isGuest {
		baseOpts = append(baseOpts, "guest", "sec=none")
	} else {
		if cfg.Options.Security != "" {
			baseOpts = append(baseOpts, "sec="+cfg.Options.Security)
		}
		baseOpts = append(baseOpts, "credentials="+credFile)
	}
	baseOpts = append(baseOpts, fmt.Sprintf("uid=%s", cfg.Options.UID))
	baseOpts = append(baseOpts, fmt.Sprintf("gid=%s", cfg.Options.GID))
	baseOpts = append(baseOpts, fmt.Sprintf("file_mode=%s", cfg.Options.FileMode))
	baseOpts = append(baseOpts, fmt.Sprintf("dir_mode=%s", cfg.Options.DirMode))
	baseOpts = append(baseOpts, "_netdev")

	// Probe for a dialect that actually mounts, then release it — the automount
	// will remount on demand. The version that worked is baked into the unit.
	// Credentials come from the cred file (or it is a guest mount), so no
	// PASSWD env is needed for the probe.
	version, err := mountWithNegotiation(cfg.Share.Path, mountPoint, baseOpts, versionsToTry(cfg.Options.Version), "")
	if err != nil {
		return fmt.Errorf("could not negotiate an SMB dialect for %s: %w", cfg.Share.Path, err)
	}
	sudoRun("umount", "-l", mountPoint)
	mountOpts := append([]string{"vers=" + version}, baseOpts...)

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

	// Trigger the mount immediately so it shows up in /proc/mounts. Starting the
	// .mount unit directly while the .automount is active fails with ENODEV;
	// reading the directory lets autofs perform the mount the normal way.
	sudoRun("ls", mountPoint)

	return nil
}

func sudoRun(args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := sysExec(ctx, nil, "", "sudo", append([]string{"-n"}, args...)...)
	if err != nil {
		outStr := strings.TrimSpace(out)
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
	out, err := sysExec(ctx, nil, content, "sudo", "-n", "tee", path)
	if err != nil {
		errStr := strings.TrimSpace(out)
		if strings.Contains(errStr, "password is required") || strings.Contains(errStr, "a password is required") {
			return fmt.Errorf("sudo requires a password — run 'sudo -v' in another terminal first")
		}
		return fmt.Errorf("write %s: %s", path, errStr)
	}
	return nil
}

func systemdEscapePath(path string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := sysExec(ctx, nil, "", "systemd-escape", "-p", path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
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
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := sysExec(ctx, nil, "", "systemctl", "list-units", "--type=automount", "--all", "--no-legend", "--no-pager")
	if err != nil {
		return nil, err
	}

	var configs []AutomountConfig
	scanner := bufio.NewScanner(strings.NewReader(out))
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
		showOut, err := sysExec(ctx, nil, "", "systemctl", "show", mountUnit, "--property=What,Where,Options")
		if err != nil {
			continue
		}

		cfg, ok := parseAutomountShow(showOut)
		if !ok {
			continue
		}
		configs = append(configs, cfg)
	}
	return configs, nil
}

// parseAutomountShow turns `systemctl show <unit>.mount --property=What,Where,Options`
// key=value output into an AutomountConfig. ok is false when the What property
// is not a CIFS UNC path.
func parseAutomountShow(showOutput string) (AutomountConfig, bool) {
	props := make(map[string]string)
	for _, line := range strings.Split(showOutput, "\n") {
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			props[parts[0]] = parts[1]
		}
	}

	what := props["What"]
	if !strings.HasPrefix(what, "//") {
		return AutomountConfig{}, false
	}

	host, name := splitUNCPath(what)
	return AutomountConfig{
		Share: Share{
			Host:       host,
			Name:       name,
			Path:       what,
			MountPoint: props["Where"],
		},
		MountPoint: props["Where"],
	}, true
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

	_, err := sysExec(ctx, nil, "", "smbclient", "-L", host, "-N", "--no-pass", "-m", "SMB3")
	elapsed := time.Since(start)
	if err != nil {
		if _, err2 := sysExec(ctx, nil, "", "ping", "-c", "1", "-W", "2", host); err2 != nil {
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
	return parseFstab(string(data)), nil
}

// parseFstab extracts cifs/smb3 entries from /etc/fstab-style content.
func parseFstab(data string) []Share {
	var shares []Share
	scanner := bufio.NewScanner(strings.NewReader(data))
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
		host, name := splitUNCPath(path)
		shares = append(shares, Share{
			Host:       host,
			Name:       name,
			Path:       path,
			MountPoint: fields[1],
			Type:       ShareTypeDisk,
		})
	}
	return shares
}

func AddFstabEntry(share Share, opts MountOptions) error {
	mountPoint := opts.MountPoint
	if mountPoint == "" {
		mountPoint = filepath.Join("/mnt/smb", share.Host, share.Name)
	}

	if err := sudoRun("mkdir", "-p", mountPoint); err != nil {
		return fmt.Errorf("failed to create mount point %s: %w", mountPoint, err)
	}

	// baseOpts excludes vers=; the concrete dialect is resolved by probing so
	// the fstab entry pins a version the server supports (fstab cannot carry
	// the "auto" sentinel — it must be a literal mount option).
	var baseOpts []string
	if opts.Creds.Username != "" {
		credFile := fmt.Sprintf("/etc/samba/credentials/%s-%s", share.Host, share.Name)
		if err := sudoRun("mkdir", "-p", "/etc/samba/credentials"); err != nil {
			return fmt.Errorf("failed to create credentials dir: %w", err)
		}
		credContent := fmt.Sprintf("username=%s\npassword=%s\n", opts.Creds.Username, opts.Creds.Password)
		if opts.Creds.Domain != "" {
			credContent += fmt.Sprintf("domain=%s\n", opts.Creds.Domain)
		}
		if err := sudoWrite(credFile, credContent); err != nil {
			return fmt.Errorf("failed to write credentials: %w", err)
		}
		sudoRun("chmod", "600", credFile)
		baseOpts = append(baseOpts, "credentials="+credFile)
	} else {
		baseOpts = append(baseOpts, "guest")
	}
	if opts.UID != "" {
		baseOpts = append(baseOpts, "uid="+opts.UID)
	}
	if opts.GID != "" {
		baseOpts = append(baseOpts, "gid="+opts.GID)
	}

	version, err := mountWithNegotiation(share.Path, mountPoint, baseOpts, versionsToTry(opts.Version), "")
	if err != nil {
		return fmt.Errorf("could not negotiate an SMB dialect for %s: %w", share.Path, err)
	}
	sudoRun("umount", "-l", mountPoint)

	mountOpts := append([]string{"vers=" + version}, baseOpts...)
	mountOpts = append(mountOpts, "_netdev", "nofail")

	entry := fmt.Sprintf("%s\t%s\tcifs\t%s\t0\t0\n",
		share.Path, mountPoint, strings.Join(mountOpts, ","))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if out, err := sysExec(ctx, nil, "", "sudo", "bash", "-c", fmt.Sprintf("echo %q >> /etc/fstab", entry)); err != nil {
		return fmt.Errorf("failed to add fstab entry: %s: %w", out, err)
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
