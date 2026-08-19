package smb

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
)

// --- test harness: a fake for the sysExec command seam ---------------------

type recordedCall struct {
	name  string
	args  []string
	stdin string
	env   []string
}

// fakeSys records every command issued through sysExec and returns scripted
// output/errors via respond. Because it swaps the package-global sysExec,
// tests using it must not run in parallel.
type fakeSys struct {
	calls   []recordedCall
	respond func(name string, args []string, stdin string) (string, error)
}

func (f *fakeSys) exec(_ context.Context, env []string, stdin, name string, args ...string) (string, error) {
	f.calls = append(f.calls, recordedCall{
		name:  name,
		args:  append([]string(nil), args...),
		stdin: stdin,
		env:   append([]string(nil), env...),
	})
	if f.respond == nil {
		return "", nil
	}
	return f.respond(name, args, stdin)
}

// install swaps in the fake and returns a restore func for defer.
func (f *fakeSys) install() func() {
	prev := sysExec
	sysExec = f.exec
	return func() { sysExec = prev }
}

func (f *fakeSys) lines() []string {
	out := make([]string, 0, len(f.calls))
	for _, c := range f.calls {
		out = append(out, c.name+" "+strings.Join(c.args, " "))
	}
	return out
}

// index returns the position of the first recorded command whose rendered line
// contains sub, or -1.
func (f *fakeSys) index(sub string) int {
	for i, l := range f.lines() {
		if strings.Contains(l, sub) {
			return i
		}
	}
	return -1
}

func (f *fakeSys) contains(sub string) bool { return f.index(sub) >= 0 }

// stdinForTee returns the stdin piped to `sudo -n tee <path>` where path ends
// with the given suffix (i.e. the content written to a unit/cred file).
func (f *fakeSys) stdinForTee(pathSuffix string) (string, bool) {
	for _, c := range f.calls {
		if c.name == "sudo" && len(c.args) >= 3 && c.args[1] == "tee" && strings.HasSuffix(c.args[2], pathSuffix) {
			return c.stdin, true
		}
	}
	return "", false
}

// mountAttempts returns the ordered list of vers= values attempted via
// `sudo -n mount -t cifs ...`.
func (f *fakeSys) mountAttempts() []string {
	var vers []string
	for _, c := range f.calls {
		if c.name != "sudo" {
			continue
		}
		// args: -n mount -t cifs <share> <mp> -o <optString>
		if len(c.args) >= 8 && c.args[1] == "mount" && c.args[6] == "-o" {
			for _, opt := range strings.Split(c.args[7], ",") {
				if strings.HasPrefix(opt, "vers=") {
					vers = append(vers, strings.TrimPrefix(opt, "vers="))
				}
			}
		}
	}
	return vers
}

// optStringFor returns the -o option string of the mount attempt at vers=v.
func (f *fakeSys) optStringFor(v string) string {
	for _, c := range f.calls {
		if c.name == "sudo" && len(c.args) >= 8 && c.args[1] == "mount" && c.args[6] == "-o" &&
			strings.Contains(c.args[7], "vers="+v+",") || (c.name == "sudo" && len(c.args) >= 8 && c.args[1] == "mount" && c.args[7] == "vers="+v) {
			return c.args[7]
		}
	}
	return ""
}

// mountResponder returns a respond func that fails every mount attempt except
// vers=okVersion (which succeeds); non-mount commands succeed silently.
func mountResponder(okVersion string) func(name string, args []string, stdin string) (string, error) {
	return func(name string, args []string, _ string) (string, error) {
		if name == "sudo" && len(args) >= 8 && args[1] == "mount" && args[6] == "-o" {
			if strings.Contains(args[7], "vers="+okVersion) {
				return "", nil
			}
			return "mount error(22): Invalid argument", fmt.Errorf("exit status 32")
		}
		return "", nil
	}
}

// --- pure logic -------------------------------------------------------------

func TestVersionsToTry(t *testing.T) {
	cases := map[string][]string{
		"":          autoDialects,
		AutoVersion: autoDialects,
		"3.0":       {"3.0"},
		"1.0":       {"1.0"},
		"2.1":       {"2.1"},
	}
	for in, want := range cases {
		if got := versionsToTry(in); !reflect.DeepEqual(got, want) {
			t.Errorf("versionsToTry(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestIsDialectError(t *testing.T) {
	dialect := []string{
		"mount error(22): Invalid argument",
		"something Invalid argument here",
		"error(22)",
	}
	for _, s := range dialect {
		if !isDialectError(s) {
			t.Errorf("isDialectError(%q) = false, want true", s)
		}
	}
	// Non-dialect errors must NOT be treated as retryable — this is the guard
	// that keeps credential and ENODEV failures from being masked by laddering.
	notDialect := []string{
		"mount error(13): Permission denied",
		"Couldn't chdir to /mnt/x: No such device",
		"host is down",
		"",
	}
	for _, s := range notDialect {
		if isDialectError(s) {
			t.Errorf("isDialectError(%q) = true, want false", s)
		}
	}
}

func TestDefaultMountOptions(t *testing.T) {
	o := DefaultMountOptions()
	if o.Version != AutoVersion {
		t.Errorf("Version = %q, want %q", o.Version, AutoVersion)
	}
	if o.Security != "ntlmssp" {
		t.Errorf("Security = %q, want ntlmssp", o.Security)
	}
	if o.FileMode != "0755" || o.DirMode != "0755" {
		t.Errorf("modes = %q/%q, want 0755/0755", o.FileMode, o.DirMode)
	}
	if o.UID != fmt.Sprintf("%d", os.Getuid()) {
		t.Errorf("UID = %q, want %d", o.UID, os.Getuid())
	}
}

func TestSplitUNCPath(t *testing.T) {
	cases := []struct {
		in, host, name string
	}{
		{"//10.0.1.97/junkdrawer", "10.0.1.97", "junkdrawer"},
		{"//host", "host", ""},
		{"//host/a/b", "host", "a/b"},
		{"", "", ""},
	}
	for _, c := range cases {
		h, n := splitUNCPath(c.in)
		if h != c.host || n != c.name {
			t.Errorf("splitUNCPath(%q) = (%q,%q), want (%q,%q)", c.in, h, n, c.host, c.name)
		}
	}
}

func TestSystemdUnitName(t *testing.T) {
	if got := systemdUnitName("/mnt/smb/10.0.1.97/junkdrawer"); got != "mnt-smb-10.0.1.97-junkdrawer" {
		t.Errorf("systemdUnitName = %q", got)
	}
}

func TestFormatSize(t *testing.T) {
	cases := map[uint64]string{
		0:                         "0 B",
		512:                       "512 B",
		1024:                      "1.0 KB",
		1024 * 1024:               "1.0 MB",
		1024 * 1024 * 1024:        "1.0 GB",
		1024 * 1024 * 1024 * 1024: "1.0 TB",
	}
	for in, want := range cases {
		if got := FormatSize(in); got != want {
			t.Errorf("FormatSize(%d) = %q, want %q", in, got, want)
		}
	}
}

// --- unitNameForPath (systemd-escape seam) ---------------------------------

func TestUnitNameForPath(t *testing.T) {
	// systemd-escape available: use its output.
	f := &fakeSys{respond: func(name string, args []string, _ string) (string, error) {
		if name == "systemd-escape" {
			return "mnt-smb-10.0.1.97-junkdrawer\n", nil
		}
		return "", nil
	}}
	defer f.install()()
	if got := unitNameForPath("/mnt/smb/10.0.1.97/junkdrawer"); got != "mnt-smb-10.0.1.97-junkdrawer" {
		t.Errorf("with systemd-escape: got %q", got)
	}

	// systemd-escape failing: fall back to manual escape.
	f2 := &fakeSys{respond: func(name string, args []string, _ string) (string, error) {
		if name == "systemd-escape" {
			return "", fmt.Errorf("not found")
		}
		return "", nil
	}}
	defer f2.install()()
	if got := unitNameForPath("/mnt/smb/h/s"); got != "mnt-smb-h-s" {
		t.Errorf("fallback: got %q", got)
	}
}

// --- parsers ----------------------------------------------------------------

func TestParseSmbclientList(t *testing.T) {
	out := `
	Sharename       Type      Comment
	---------       ----      -------
	zrkshare        Disk      stuff to share
	junkdrawer      Disk      random shit
	printer1        Printer   HP
	IPC$            IPC       IPC Service

SMB1 disabled -- no workgroup available
`
	shares := parseSmbclientList("10.0.1.97", out)
	if len(shares) != 4 {
		t.Fatalf("got %d shares, want 4: %+v", len(shares), shares)
	}
	if shares[0].Name != "zrkshare" || shares[0].Type != ShareTypeDisk {
		t.Errorf("share0 = %+v", shares[0])
	}
	if shares[0].Comment != "stuff to share" {
		t.Errorf("comment = %q", shares[0].Comment)
	}
	if shares[0].Path != "//10.0.1.97/zrkshare" {
		t.Errorf("path = %q", shares[0].Path)
	}
	if shares[2].Type != ShareTypePrinter {
		t.Errorf("printer type = %q", shares[2].Type)
	}
	if shares[3].Type != ShareTypeIPC {
		t.Errorf("ipc type = %q", shares[3].Type)
	}
}

func TestParseAvahi(t *testing.T) {
	out := `+;eth0;IPv4;NAS;_smb._tcp;local
=;eth0;IPv4;NAS;_smb._tcp;local;nas.local;10.0.1.97;445;
=;eth0;IPv4;NAS;_smb._tcp;local;nas.local;10.0.1.97;445;
=;eth0;IPv4;Other;_smb._tcp;local;other.local;10.0.1.50;445;
`
	hosts := parseAvahi(out)
	if len(hosts) != 2 {
		t.Fatalf("got %d hosts, want 2 (deduped): %+v", len(hosts), hosts)
	}
	if hosts[0].IP != "10.0.1.97" || hosts[0].Name != "NAS" {
		t.Errorf("host0 = %+v", hosts[0])
	}
}

func TestParseNMB(t *testing.T) {
	out := `Name query response received
10.0.1.97       NAS
10.0.1.97       NAS
10.0.1.50       OTHER
`
	hosts := parseNMB(out)
	if len(hosts) != 2 {
		t.Fatalf("got %d hosts, want 2: %+v", len(hosts), hosts)
	}
	if hosts[0].IP != "10.0.1.97" || hosts[0].Name != "NAS" {
		t.Errorf("host0 = %+v", hosts[0])
	}
}

func TestParseCifsMounts(t *testing.T) {
	data := `sysfs /sys sysfs rw 0 0
//10.0.1.97/junkdrawer /mnt/smb/10.0.1.97/junkdrawer cifs rw,vers=2.0 0 0
//10.0.1.97/moviebox /mnt/smb/10.0.1.97/moviebox smb3 rw 0 0
tmpfs /tmp tmpfs rw 0 0
`
	shares := parseCifsMounts(data)
	if len(shares) != 2 {
		t.Fatalf("got %d, want 2: %+v", len(shares), shares)
	}
	if shares[0].Host != "10.0.1.97" || shares[0].Name != "junkdrawer" {
		t.Errorf("share0 = %+v", shares[0])
	}
	if shares[0].MountPoint != "/mnt/smb/10.0.1.97/junkdrawer" {
		t.Errorf("mp = %q", shares[0].MountPoint)
	}
	if !shares[0].IsMounted {
		t.Error("IsMounted should be true")
	}
}

func TestParseFstab(t *testing.T) {
	data := `# a comment
UUID=xxx / ext4 defaults 0 1
//10.0.1.97/junkdrawer /mnt/smb/10.0.1.97/junkdrawer cifs credentials=/x,vers=2.0 0 0

//10.0.1.97/moviebox /mnt/mb smb3 guest 0 0
`
	shares := parseFstab(data)
	if len(shares) != 2 {
		t.Fatalf("got %d, want 2: %+v", len(shares), shares)
	}
	if shares[0].Name != "junkdrawer" || shares[0].MountPoint != "/mnt/smb/10.0.1.97/junkdrawer" {
		t.Errorf("share0 = %+v", shares[0])
	}
}

func TestParseAutomountShow(t *testing.T) {
	ok := "What=//10.0.1.97/junkdrawer\nWhere=/mnt/smb/10.0.1.97/junkdrawer\nOptions=vers=2.0,credentials=/x\n"
	cfg, good := parseAutomountShow(ok)
	if !good {
		t.Fatal("expected ok=true")
	}
	if cfg.Share.Host != "10.0.1.97" || cfg.Share.Name != "junkdrawer" {
		t.Errorf("share = %+v", cfg.Share)
	}
	if cfg.MountPoint != "/mnt/smb/10.0.1.97/junkdrawer" {
		t.Errorf("mp = %q", cfg.MountPoint)
	}

	// Non-UNC What (e.g. binfmt_misc automount) → not a share.
	if _, good := parseAutomountShow("What=binfmt_misc\nWhere=/proc/sys/fs/binfmt_misc\n"); good {
		t.Error("expected ok=false for non-UNC What")
	}
}

func TestParseDiskUsage(t *testing.T) {
	out := "     Size      Used     Avail\n1000000000 400000000 600000000\n"
	total, used, free, ok := parseDiskUsage(out)
	if !ok {
		t.Fatal("ok=false")
	}
	if total != 1000000000 || used != 400000000 || free != 600000000 {
		t.Errorf("got %d/%d/%d", total, used, free)
	}
	if _, _, _, ok := parseDiskUsage("only-header\n"); ok {
		t.Error("single line should be ok=false")
	}
}

func TestParseSmbclientList_EdgeCases(t *testing.T) {
	// Unknown type → defaults to Disk; a single-field row is skipped; a blank
	// line closes the share section so trailing text is ignored.
	out := `
	Sharename       Type      Comment
	---------       ----      -------
	weird           Bananas
	oneword
	real            Disk

trailing junk that is not a share
`
	shares := parseSmbclientList("h", out)
	if len(shares) != 2 {
		t.Fatalf("got %d shares, want 2: %+v", len(shares), shares)
	}
	if shares[0].Name != "weird" || shares[0].Type != ShareTypeDisk {
		t.Errorf("unknown type should default to Disk: %+v", shares[0])
	}
	if shares[1].Name != "real" {
		t.Errorf("share1 = %+v", shares[1])
	}
}

func TestParseCifsMounts_ShortLineSkipped(t *testing.T) {
	shares := parseCifsMounts("tooshort\n//h/s /mnt cifs rw 0 0\n")
	if len(shares) != 1 {
		t.Fatalf("got %d, want 1", len(shares))
	}
}

func TestParseFstab_ShortLineSkipped(t *testing.T) {
	shares := parseFstab("badline\n//h/s /mnt cifs guest 0 0\n")
	if len(shares) != 1 {
		t.Fatalf("got %d, want 1", len(shares))
	}
}

func TestParseDiskUsage_ShortDataLine(t *testing.T) {
	if _, _, _, ok := parseDiskUsage("Size Used Avail\n100 200\n"); ok {
		t.Error("a two-field data line should be ok=false")
	}
}

// --- mountWithNegotiation (the core bug lives here) ------------------------

func TestMountWithNegotiation_LaddersToWorkingDialect(t *testing.T) {
	f := &fakeSys{respond: mountResponder("2.0")}
	defer f.install()()

	v, err := mountWithNegotiation("//h/s", "/mnt/x", []string{"guest", "sec=none"}, autoDialects, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v != "2.0" {
		t.Errorf("negotiated %q, want 2.0", v)
	}
	if got := f.mountAttempts(); !reflect.DeepEqual(got, []string{"3.1.1", "3.0", "2.1", "2.0"}) {
		t.Errorf("attempts = %v, want full ladder in order", got)
	}
}

func TestMountWithNegotiation_StopsAtFirstSuccess(t *testing.T) {
	f := &fakeSys{respond: mountResponder("3.1.1")}
	defer f.install()()

	v, err := mountWithNegotiation("//h/s", "/mnt/x", nil, autoDialects, "")
	if err != nil || v != "3.1.1" {
		t.Fatalf("got (%q,%v), want (3.1.1,nil)", v, err)
	}
	if got := f.mountAttempts(); !reflect.DeepEqual(got, []string{"3.1.1"}) {
		t.Errorf("attempts = %v, want just [3.1.1]", got)
	}
}

func TestMountWithNegotiation_AbortsOnNonDialectError(t *testing.T) {
	// A credential/ENODEV-style error is NOT error(22): negotiation must stop
	// immediately rather than mask it by trying lower dialects.
	f := &fakeSys{respond: func(name string, args []string, _ string) (string, error) {
		if name == "sudo" && len(args) >= 2 && args[1] == "mount" {
			return "mount error(13): Permission denied", fmt.Errorf("exit status 32")
		}
		return "", nil
	}}
	defer f.install()()

	_, err := mountWithNegotiation("//h/s", "/mnt/x", nil, autoDialects, "")
	if err == nil {
		t.Fatal("expected error")
	}
	if got := f.mountAttempts(); len(got) != 1 {
		t.Errorf("attempts = %v, want exactly 1 (no laddering past a non-dialect error)", got)
	}
}

func TestMountWithNegotiation_PasswordRequired(t *testing.T) {
	f := &fakeSys{respond: func(name string, args []string, _ string) (string, error) {
		return "sudo: a password is required", fmt.Errorf("exit status 1")
	}}
	defer f.install()()

	_, err := mountWithNegotiation("//h/s", "/mnt/x", nil, autoDialects, "")
	if err == nil || !strings.Contains(err.Error(), "sudo requires a password") {
		t.Fatalf("err = %v, want sudo-password message", err)
	}
}

func TestMountWithNegotiation_AllFail(t *testing.T) {
	f := &fakeSys{respond: mountResponder("nope")} // nothing succeeds
	defer f.install()()

	_, err := mountWithNegotiation("//h/s", "/mnt/x", nil, autoDialects, "")
	if err == nil || !strings.Contains(err.Error(), "vers=2.0") {
		t.Fatalf("err = %v, want last (vers=2.0) failure", err)
	}
}

// --- clearAutomountAt -------------------------------------------------------

func TestClearAutomountAt(t *testing.T) {
	f := &fakeSys{respond: func(name string, args []string, _ string) (string, error) {
		if name == "systemd-escape" {
			return "mnt-smb-h-s", nil
		}
		return "", nil
	}}
	defer f.install()()

	clearAutomountAt("/mnt/smb/h/s")

	iAuto := f.index("systemctl stop mnt-smb-h-s.automount")
	iMount := f.index("systemctl stop mnt-smb-h-s.mount")
	iUmount := f.index("umount -l /mnt/smb/h/s")
	if iAuto < 0 || iMount < 0 || iUmount < 0 {
		t.Fatalf("missing a teardown command: %v", f.lines())
	}
	if !(iAuto < iMount && iMount < iUmount) {
		t.Errorf("teardown order wrong: automount=%d mount=%d umount=%d", iAuto, iMount, iUmount)
	}
}

// --- MountShare -------------------------------------------------------------

func TestMountShare_GuestClearsAutofsThenMounts(t *testing.T) {
	f := &fakeSys{respond: mountResponder("2.0")}
	f.respond = func(name string, args []string, stdin string) (string, error) {
		if name == "systemd-escape" {
			return "mnt-smb-h-s", nil
		}
		return mountResponder("2.0")(name, args, stdin)
	}
	defer f.install()()

	err := MountShare(Share{Host: "h", Name: "s", Path: "//h/s"}, MountOptions{
		MountPoint: "/mnt/smb/h/s",
		UID:        "1000", GID: "1000", FileMode: "0755", DirMode: "0755",
	})
	if err != nil {
		t.Fatalf("MountShare error: %v", err)
	}

	// Autofs cleared before mkdir before the first mount attempt.
	iClear := f.index("systemctl stop mnt-smb-h-s.automount")
	iMkdir := f.index("mkdir -p /mnt/smb/h/s")
	iMount := f.index("mount -t cifs //h/s")
	if !(iClear >= 0 && iClear < iMkdir && iMkdir < iMount) {
		t.Fatalf("order wrong (clear=%d mkdir=%d mount=%d): %v", iClear, iMkdir, iMount, f.lines())
	}
	// Guest options present, negotiation reached 2.0.
	opt := f.optStringFor("2.0")
	if !strings.Contains(opt, "guest") || !strings.Contains(opt, "sec=none") {
		t.Errorf("guest opts missing: %q", opt)
	}
	if !reflect.DeepEqual(f.mountAttempts(), []string{"3.1.1", "3.0", "2.1", "2.0"}) {
		t.Errorf("attempts = %v", f.mountAttempts())
	}
}

func TestMountShare_CredsPassPasswordViaEnv(t *testing.T) {
	var mountEnv []string
	f := &fakeSys{}
	f.respond = func(name string, args []string, stdin string) (string, error) {
		if name == "systemd-escape" {
			return "mnt-smb-h-s", nil
		}
		if name == "sudo" && len(args) >= 2 && args[1] == "mount" {
			// capture env for the (successful) attempt
			return "", nil
		}
		return "", nil
	}
	// wrap exec to capture env of the mount attempt
	prev := sysExec
	sysExec = func(ctx context.Context, env []string, stdin, n string, a ...string) (string, error) {
		if n == "sudo" && len(a) >= 2 && a[1] == "mount" {
			mountEnv = env
		}
		f.calls = append(f.calls, recordedCall{name: n, args: append([]string(nil), a...), stdin: stdin, env: env})
		return f.respond(n, a, stdin)
	}
	defer func() { sysExec = prev }()

	err := MountShare(Share{Path: "//h/s"}, MountOptions{
		MountPoint: "/mnt/smb/h/s",
		Security:   "ntlmssp",
		Creds:      Credentials{Username: "zrk", Password: "secret"},
	})
	if err != nil {
		t.Fatalf("MountShare error: %v", err)
	}
	opt := f.optStringFor("3.1.1")
	if !strings.Contains(opt, "username=zrk") || !strings.Contains(opt, "sec=ntlmssp") {
		t.Errorf("cred opts missing: %q", opt)
	}
	if strings.Contains(opt, "guest") {
		t.Errorf("guest must not appear with creds: %q", opt)
	}
	found := false
	for _, e := range mountEnv {
		if e == "PASSWD=secret" {
			found = true
		}
	}
	if !found {
		t.Errorf("PASSWD env not passed to mount: %v", mountEnv)
	}
}

func TestMountShare_MkdirFailureSurfaced(t *testing.T) {
	f := &fakeSys{respond: func(name string, args []string, _ string) (string, error) {
		if name == "systemd-escape" {
			return "mnt-smb-h-s", nil
		}
		if name == "sudo" && len(args) >= 2 && args[1] == "mkdir" {
			return "mkdir: permission denied", fmt.Errorf("exit status 1")
		}
		return "", nil
	}}
	defer f.install()()

	err := MountShare(Share{Path: "//h/s"}, MountOptions{MountPoint: "/mnt/smb/h/s"})
	if err == nil || !strings.Contains(err.Error(), "failed to create mount point") {
		t.Fatalf("err = %v, want mkdir failure", err)
	}
	if f.contains("mount -t cifs") {
		t.Error("must not attempt mount after mkdir fails")
	}
}

// --- CreateAutomount --------------------------------------------------------

func TestCreateAutomount_BakesNegotiatedVersionAndTriggersViaAutofs(t *testing.T) {
	f := &fakeSys{}
	f.respond = func(name string, args []string, stdin string) (string, error) {
		if name == "systemd-escape" {
			return "mnt-smb-h-s", nil
		}
		return mountResponder("2.0")(name, args, stdin)
	}
	defer f.install()()

	err := CreateAutomount(AutomountConfig{
		Share:      Share{Host: "h", Name: "s", Path: "//h/s"},
		MountPoint: "/mnt/smb/h/s",
		Options: MountOptions{
			UID: "1000", GID: "1000", FileMode: "0755", DirMode: "0755",
			Creds: Credentials{Username: "zrk", Password: "secret"},
		},
	})
	if err != nil {
		t.Fatalf("CreateAutomount error: %v", err)
	}

	// Probe laddered to the working dialect.
	if !reflect.DeepEqual(f.mountAttempts(), []string{"3.1.1", "3.0", "2.1", "2.0"}) {
		t.Errorf("probe attempts = %v", f.mountAttempts())
	}

	// The .mount unit was written pinning the negotiated vers=2.0 (NOT 3.x).
	unit, ok := f.stdinForTee(".mount")
	if !ok {
		t.Fatal("no .mount unit written")
	}
	if !strings.Contains(unit, "vers=2.0,") {
		t.Errorf(".mount unit missing vers=2.0: %q", unit)
	}
	if strings.Contains(unit, "vers=3") {
		t.Errorf(".mount unit pinned a rejected dialect: %q", unit)
	}
	if !strings.Contains(unit, "Type=cifs") || !strings.Contains(unit, "_netdev") {
		t.Errorf(".mount unit malformed: %q", unit)
	}

	// An .automount unit was written too.
	if _, ok := f.stdinForTee(".automount"); !ok {
		t.Error("no .automount unit written")
	}

	// Lifecycle: daemon-reload, enable + start the automount.
	if !f.contains("systemctl daemon-reload") {
		t.Error("missing daemon-reload")
	}
	if !f.contains("systemctl enable mnt-smb-h-s.automount") {
		t.Error("missing enable")
	}
	if !f.contains("systemctl start mnt-smb-h-s.automount") {
		t.Error("missing start automount")
	}

	// Regression guard for the ENODEV fix: the immediate trigger must NOT be
	// `systemctl start <unit>.mount` (which collides with the active autofs);
	// it must access the directory so autofs performs the mount.
	if f.contains("systemctl start mnt-smb-h-s.mount") {
		t.Error("must not `systemctl start .mount` while automount is active")
	}
	if !f.contains("ls /mnt/smb/h/s") {
		t.Error("expected autofs-safe trigger `ls <mountPoint>`")
	}
}

func TestCreateAutomount_ProbeFailureReturnsError(t *testing.T) {
	f := &fakeSys{}
	f.respond = func(name string, args []string, stdin string) (string, error) {
		if name == "systemd-escape" {
			return "mnt-smb-h-s", nil
		}
		return mountResponder("nope")(name, args, stdin) // all dialects fail
	}
	defer f.install()()

	err := CreateAutomount(AutomountConfig{
		Share:      Share{Path: "//h/s"},
		MountPoint: "/mnt/smb/h/s",
		Options:    MountOptions{Creds: Credentials{Username: "zrk", Password: "x"}},
	})
	if err == nil || !strings.Contains(err.Error(), "could not negotiate an SMB dialect") {
		t.Fatalf("err = %v, want negotiation failure", err)
	}
	// No unit files should be written if the probe never succeeds.
	if _, ok := f.stdinForTee(".mount"); ok {
		t.Error("must not write .mount unit when negotiation fails")
	}
}

// --- ListShares -------------------------------------------------------------

func TestListShares(t *testing.T) {
	out := `
	Sharename       Type      Comment
	---------       ----      -------
	zrkshare        Disk      stuff
	IPC$            IPC       IPC Service
`
	f := &fakeSys{respond: func(name string, args []string, _ string) (string, error) {
		return out, nil
	}}
	defer f.install()()

	shares, err := ListShares(context.Background(), "10.0.1.97", Credentials{})
	if err != nil {
		t.Fatalf("ListShares error: %v", err)
	}
	if len(shares) != 2 || shares[0].Name != "zrkshare" {
		t.Fatalf("shares = %+v", shares)
	}
	// Guest listing uses -N/--no-pass and no -U.
	if !f.contains("smbclient -L 10.0.1.97 -N --no-pass") {
		t.Errorf("unexpected smbclient invocation: %v", f.lines())
	}
}

func TestListShares_CredsUseUsernameFlag(t *testing.T) {
	f := &fakeSys{respond: func(name string, args []string, stdin string) (string, error) {
		if stdin != "pw\n" {
			t.Errorf("password not piped via stdin, got %q", stdin)
		}
		return "", nil
	}}
	defer f.install()()

	if _, err := ListShares(context.Background(), "h", Credentials{Username: "zrk", Password: "pw", Domain: "WG"}); err != nil {
		t.Fatalf("err: %v", err)
	}
	if !f.contains("smbclient -L h -U zrk -W WG") {
		t.Errorf("expected -U/-W invocation: %v", f.lines())
	}
}

func TestListShares_ErrorWithNoOutput(t *testing.T) {
	f := &fakeSys{respond: func(name string, args []string, _ string) (string, error) {
		return "", fmt.Errorf("connection refused")
	}}
	defer f.install()()

	if _, err := ListShares(context.Background(), "h", Credentials{}); err == nil {
		t.Fatal("expected error when smbclient fails with empty output")
	}
}

// --- fillDiskUsage ----------------------------------------------------------

func TestFillDiskUsage(t *testing.T) {
	f := &fakeSys{respond: func(name string, args []string, _ string) (string, error) {
		if name == "df" {
			return "Size Used Avail\n1000 400 600\n", nil
		}
		return "", nil
	}}
	defer f.install()()

	s := &Share{MountPoint: "/mnt/smb/h/s"}
	fillDiskUsage(s)
	if s.SizeTotal != 1000 || s.SizeUsed != 400 || s.SizeFree != 600 {
		t.Errorf("usage = %d/%d/%d", s.SizeTotal, s.SizeUsed, s.SizeFree)
	}
}

func TestFillDiskUsage_NoMountPointIsNoop(t *testing.T) {
	f := &fakeSys{}
	defer f.install()()
	s := &Share{}
	fillDiskUsage(s)
	if len(f.calls) != 0 {
		t.Errorf("expected no df call for empty mount point, got %v", f.lines())
	}
}

func TestFillDiskUsage_ErrorLeavesZero(t *testing.T) {
	f := &fakeSys{respond: func(name string, args []string, _ string) (string, error) {
		return "", fmt.Errorf("df: no such file")
	}}
	defer f.install()()
	s := &Share{MountPoint: "/mnt/x"}
	fillDiskUsage(s)
	if s.SizeTotal != 0 {
		t.Errorf("expected zero usage on error, got %d", s.SizeTotal)
	}
}

// --- CheckConnectivity ------------------------------------------------------

func TestCheckConnectivity_OK(t *testing.T) {
	f := &fakeSys{respond: func(name string, args []string, _ string) (string, error) {
		return "", nil // smbclient succeeds
	}}
	defer f.install()()
	if _, err := CheckConnectivity("h"); err != nil {
		t.Fatalf("expected reachable, got %v", err)
	}
}

func TestCheckConnectivity_SMBDownButPingUp(t *testing.T) {
	f := &fakeSys{respond: func(name string, args []string, _ string) (string, error) {
		if name == "smbclient" {
			return "", fmt.Errorf("NT_STATUS_CONNECTION_REFUSED")
		}
		return "", nil // ping succeeds
	}}
	defer f.install()()
	_, err := CheckConnectivity("h")
	if err == nil || !strings.Contains(err.Error(), "SMB service unavailable") {
		t.Fatalf("err = %v, want SMB service unavailable", err)
	}
}

func TestCheckConnectivity_HostUnreachable(t *testing.T) {
	f := &fakeSys{respond: func(name string, args []string, _ string) (string, error) {
		return "", fmt.Errorf("down") // both smbclient and ping fail
	}}
	defer f.install()()
	_, err := CheckConnectivity("h")
	if err == nil || !strings.Contains(err.Error(), "host unreachable") {
		t.Fatalf("err = %v, want host unreachable", err)
	}
}

// --- GetAutomounts ----------------------------------------------------------

func TestGetAutomounts(t *testing.T) {
	list := "mnt-smb-h-s.automount loaded active waiting Automount /mnt/smb/h/s\n" +
		"proc-sys-fs-binfmt_misc.automount loaded active waiting Arbitrary...\n" +
		"some.service loaded active running\n"
	f := &fakeSys{respond: func(name string, args []string, _ string) (string, error) {
		if len(args) > 0 && args[0] == "list-units" {
			return list, nil
		}
		if len(args) > 0 && args[0] == "show" {
			if strings.Contains(args[1], "mnt-smb-h-s") {
				return "What=//h/s\nWhere=/mnt/smb/h/s\nOptions=vers=2.0\n", nil
			}
			return "What=binfmt_misc\nWhere=/proc/sys/fs/binfmt_misc\n", nil
		}
		return "", nil
	}}
	defer f.install()()

	cfgs, err := GetAutomounts()
	if err != nil {
		t.Fatalf("GetAutomounts error: %v", err)
	}
	// Only the CIFS UNC automount is kept; binfmt is filtered by parseAutomountShow.
	if len(cfgs) != 1 || cfgs[0].Share.Host != "h" || cfgs[0].MountPoint != "/mnt/smb/h/s" {
		t.Fatalf("cfgs = %+v", cfgs)
	}
}

func TestGetAutomounts_ListError(t *testing.T) {
	f := &fakeSys{respond: func(name string, args []string, _ string) (string, error) {
		return "", fmt.Errorf("systemctl unavailable")
	}}
	defer f.install()()
	if _, err := GetAutomounts(); err == nil {
		t.Fatal("expected error when list-units fails")
	}
}

// --- AddFstabEntry ----------------------------------------------------------

func TestAddFstabEntry_WritesNegotiatedEntry(t *testing.T) {
	f := &fakeSys{respond: mountResponder("2.0")}
	defer f.install()()

	err := AddFstabEntry(
		Share{Host: "h", Name: "s", Path: "//h/s"},
		MountOptions{MountPoint: "/mnt/smb/h/s", UID: "1000", GID: "1000"},
	)
	if err != nil {
		t.Fatalf("AddFstabEntry error: %v", err)
	}

	// The probe laddered to the working dialect and the entry pins it.
	if !reflect.DeepEqual(f.mountAttempts(), []string{"3.1.1", "3.0", "2.1", "2.0"}) {
		t.Errorf("probe attempts = %v", f.mountAttempts())
	}

	// The final append goes through `sudo bash -c "echo ... >> /etc/fstab"`.
	var entry string
	for _, c := range f.calls {
		if c.name == "sudo" && len(c.args) >= 3 && c.args[0] == "bash" && c.args[1] == "-c" {
			entry = c.args[2]
		}
	}
	if entry == "" {
		t.Fatalf("no fstab append command issued: %v", f.lines())
	}
	for _, want := range []string{"//h/s", "/mnt/smb/h/s", "cifs", "vers=2.0", "guest", "_netdev", "nofail", "/etc/fstab"} {
		if !strings.Contains(entry, want) {
			t.Errorf("fstab entry missing %q: %s", want, entry)
		}
	}
	if strings.Contains(entry, "vers=3") {
		t.Errorf("fstab entry pinned a rejected dialect: %s", entry)
	}
}

func TestAddFstabEntry_NegotiationFailureReturnsError(t *testing.T) {
	f := &fakeSys{respond: mountResponder("nope")}
	defer f.install()()

	err := AddFstabEntry(Share{Path: "//h/s"}, MountOptions{MountPoint: "/mnt/x"})
	if err == nil || !strings.Contains(err.Error(), "could not negotiate an SMB dialect") {
		t.Fatalf("err = %v, want negotiation failure", err)
	}
	if f.contains("bash -c") {
		t.Error("must not append to fstab when negotiation fails")
	}
}

func TestAddFstabEntry_AppendFails(t *testing.T) {
	f := &fakeSys{respond: func(name string, args []string, stdin string) (string, error) {
		if name == "sudo" && len(args) >= 2 && args[0] == "bash" && args[1] == "-c" {
			return "read-only fs", fmt.Errorf("exit 1")
		}
		return mountResponder("2.0")(name, args, stdin)
	}}
	defer f.install()()
	err := AddFstabEntry(Share{Path: "//h/s"}, MountOptions{MountPoint: "/mnt/x"})
	if err == nil || !strings.Contains(err.Error(), "failed to add fstab entry") {
		t.Fatalf("err = %v", err)
	}
}

func TestAddFstabEntry_CredWriteFails(t *testing.T) {
	f := &fakeSys{respond: func(name string, args []string, _ string) (string, error) {
		if name == "sudo" && len(args) >= 2 && args[1] == "tee" {
			return "denied", fmt.Errorf("exit 1")
		}
		return "", nil
	}}
	defer f.install()()
	err := AddFstabEntry(Share{Host: "h", Name: "s", Path: "//h/s"},
		MountOptions{MountPoint: "/mnt/x", Creds: Credentials{Username: "u", Password: "p"}})
	if err == nil || !strings.Contains(err.Error(), "failed to write credentials") {
		t.Fatalf("err = %v", err)
	}
}

func TestAddFstabEntry_CredsWriteCredFile(t *testing.T) {
	f := &fakeSys{respond: mountResponder("3.0")}
	defer f.install()()

	err := AddFstabEntry(
		Share{Host: "h", Name: "s", Path: "//h/s"},
		MountOptions{MountPoint: "/mnt/smb/h/s", Creds: Credentials{Username: "zrk", Password: "pw", Domain: "WG"}},
	)
	if err != nil {
		t.Fatalf("AddFstabEntry error: %v", err)
	}
	cred, ok := f.stdinForTee("/etc/samba/credentials/h-s")
	if !ok {
		t.Fatal("credentials file not written")
	}
	for _, want := range []string{"username=zrk", "password=pw", "domain=WG"} {
		if !strings.Contains(cred, want) {
			t.Errorf("cred file missing %q: %q", want, cred)
		}
	}
}

// --- MountShare failure branch ---------------------------------------------

func TestMountShare_NegotiationFails(t *testing.T) {
	f := &fakeSys{respond: mountResponder("nope")}
	defer f.install()()
	err := MountShare(Share{Path: "//h/s"}, MountOptions{MountPoint: "/mnt/x"})
	if err == nil {
		t.Fatal("expected error when no dialect mounts")
	}
}

func TestMountShare_ReadOnlyAndDomainOptions(t *testing.T) {
	f := &fakeSys{}
	f.respond = func(name string, args []string, stdin string) (string, error) {
		if name == "systemd-escape" {
			return "mnt-smb-h-s", nil
		}
		return mountResponder("3.1.1")(name, args, stdin)
	}
	defer f.install()()

	err := MountShare(Share{Path: "//h/s"}, MountOptions{
		MountPoint: "/mnt/smb/h/s",
		ReadOnly:   true,
		Creds:      Credentials{Username: "zrk", Password: "pw", Domain: "WG"},
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	opt := f.optStringFor("3.1.1")
	for _, want := range []string{"username=zrk", "domain=WG", "ro"} {
		if !strings.Contains(opt, want) {
			t.Errorf("opts missing %q: %q", want, opt)
		}
	}
}

func TestMountShare_MkdirFailAfterClear(t *testing.T) {
	f := &fakeSys{respond: func(name string, args []string, _ string) (string, error) {
		if name == "systemd-escape" {
			return "mnt-smb-h-s", nil
		}
		if name == "sudo" && len(args) >= 2 && args[1] == "mkdir" {
			return "denied", fmt.Errorf("exit 1")
		}
		return "", nil
	}}
	defer f.install()()
	err := MountShare(Share{Host: "h", Name: "s", Path: "//h/s"}, MountOptions{})
	if err == nil || !strings.Contains(err.Error(), "failed to create mount point") {
		t.Fatalf("err = %v", err)
	}
}

// --- unmount wrappers -------------------------------------------------------

func TestUnmountShare(t *testing.T) {
	f := &fakeSys{}
	defer f.install()()
	if err := UnmountShare("/mnt/x"); err != nil {
		t.Fatalf("err: %v", err)
	}
	if !f.contains("sudo -n umount /mnt/x") {
		t.Errorf("calls: %v", f.lines())
	}
}

func TestForceUnmountShare(t *testing.T) {
	f := &fakeSys{}
	defer f.install()()
	if err := ForceUnmountShare("/mnt/x"); err != nil {
		t.Fatalf("err: %v", err)
	}
	if !f.contains("sudo -n umount -l /mnt/x") {
		t.Errorf("calls: %v", f.lines())
	}
}

// --- RemoveAutomount --------------------------------------------------------

func TestRemoveAutomount(t *testing.T) {
	f := &fakeSys{respond: func(name string, args []string, _ string) (string, error) {
		if name == "systemd-escape" {
			return "mnt-smb-h-s", nil
		}
		return "", nil
	}}
	defer f.install()()

	if err := RemoveAutomount("/mnt/smb/h/s"); err != nil {
		t.Fatalf("err: %v", err)
	}
	for _, want := range []string{
		"systemctl stop mnt-smb-h-s.automount",
		"systemctl disable mnt-smb-h-s.automount",
		"rm -f /etc/systemd/system/mnt-smb-h-s.mount /etc/systemd/system/mnt-smb-h-s.automount",
		"systemctl daemon-reload",
	} {
		if !f.contains(want) {
			t.Errorf("missing %q in %v", want, f.lines())
		}
	}
}

func TestRemoveAutomount_StopFails(t *testing.T) {
	f := &fakeSys{respond: func(name string, args []string, _ string) (string, error) {
		if name == "systemd-escape" {
			return "mnt-smb-h-s", nil
		}
		if name == "sudo" && len(args) >= 3 && args[2] == "stop" {
			return "unit not loaded", fmt.Errorf("exit 1")
		}
		return "", nil
	}}
	defer f.install()()
	if err := RemoveAutomount("/mnt/smb/h/s"); err == nil {
		t.Fatal("expected error when stop fails")
	}
}

func TestRemoveAutomount_RmFails(t *testing.T) {
	// systemd-escape failing exercises the manual-fallback unit name too.
	f := &fakeSys{respond: func(name string, args []string, _ string) (string, error) {
		if name == "systemd-escape" {
			return "", fmt.Errorf("not found")
		}
		if name == "sudo" && len(args) >= 2 && args[1] == "rm" {
			return "busy", fmt.Errorf("exit 1")
		}
		return "", nil
	}}
	defer f.install()()
	err := RemoveAutomount("/mnt/smb/h/s")
	if err == nil || !strings.Contains(err.Error(), "remove unit files") {
		t.Fatalf("err = %v", err)
	}
	// Fallback unit name (manual escape) must have been used.
	if !f.contains("rm -f /etc/systemd/system/mnt-smb-h-s.mount") {
		t.Errorf("expected fallback unit name: %v", f.lines())
	}
}

func TestGetAutomounts_ShowErrorSkipsUnit(t *testing.T) {
	f := &fakeSys{respond: func(name string, args []string, _ string) (string, error) {
		if len(args) > 0 && args[0] == "list-units" {
			return "mnt-smb-h-s.automount loaded active waiting Automount /mnt/smb/h/s\n", nil
		}
		if len(args) > 0 && args[0] == "show" {
			return "", fmt.Errorf("no such unit")
		}
		return "", nil
	}}
	defer f.install()()
	cfgs, err := GetAutomounts()
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(cfgs) != 0 {
		t.Errorf("a failed `show` should skip the unit, got %+v", cfgs)
	}
}

// --- discovery --------------------------------------------------------------

func TestDiscoverAvahi(t *testing.T) {
	out := "=;eth0;IPv4;NAS;_smb._tcp;local;nas.local;10.0.1.97;445;\n"
	f := &fakeSys{respond: func(name string, args []string, _ string) (string, error) {
		return out, nil
	}}
	defer f.install()()
	hosts, err := discoverAvahi(context.Background())
	if err != nil || len(hosts) != 1 || hosts[0].IP != "10.0.1.97" {
		t.Fatalf("hosts=%+v err=%v", hosts, err)
	}
}

func TestDiscoverAvahi_Error(t *testing.T) {
	f := &fakeSys{respond: func(name string, args []string, _ string) (string, error) {
		return "", fmt.Errorf("avahi-browse: not found")
	}}
	defer f.install()()
	if _, err := discoverAvahi(context.Background()); err == nil {
		t.Fatal("expected error")
	}
}

func TestDiscoverNMB(t *testing.T) {
	f := &fakeSys{respond: func(name string, args []string, _ string) (string, error) {
		if strings.Contains(strings.Join(args, " "), "__SAMBA__") {
			return "x\n10.0.1.97       NAS\n", nil
		}
		return "", nil
	}}
	defer f.install()()
	hosts, err := discoverNMB(context.Background())
	if err != nil || len(hosts) != 1 || hosts[0].Name != "NAS" {
		t.Fatalf("hosts=%+v err=%v", hosts, err)
	}
}

func TestDiscoverHosts_MergesAndDedupes(t *testing.T) {
	// avahi and nmb both surface 10.0.1.97; it must appear once. Subnet scan is
	// short-circuited by an already-cancelled context.
	f := &fakeSys{respond: func(name string, args []string, _ string) (string, error) {
		switch name {
		case "avahi-browse":
			return "=;eth0;IPv4;NAS;_smb._tcp;local;nas.local;10.0.1.97;445;\n", nil
		case "nmblookup":
			if strings.Contains(strings.Join(args, " "), "__SAMBA__") {
				return "x\n10.0.1.97       NAS\n10.0.1.50       OTHER\n", nil
			}
			return "", nil
		}
		return "", nil
	}}
	defer f.install()()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // subnet scan returns immediately via ctx.Done()

	hosts, err := DiscoverHosts(ctx)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	ips := map[string]int{}
	for _, h := range hosts {
		ips[h.IP]++
	}
	if ips["10.0.1.97"] != 1 {
		t.Errorf("10.0.1.97 should appear once, got %d (hosts=%+v)", ips["10.0.1.97"], hosts)
	}
	if ips["10.0.1.50"] != 1 {
		t.Errorf("10.0.1.50 missing (hosts=%+v)", hosts)
	}
}

// --- /proc and /etc readers (real files on Linux) --------------------------

func TestGetMountedShares(t *testing.T) {
	f := &fakeSys{respond: func(name string, args []string, _ string) (string, error) {
		// df, if any cifs mount happens to be present
		return "Size Used Avail\n1 1 1\n", nil
	}}
	defer f.install()()
	if _, err := GetMountedShares(); err != nil {
		t.Fatalf("GetMountedShares should read /proc/mounts: %v", err)
	}
}

func TestGetFstabEntries(t *testing.T) {
	if _, err := GetFstabEntries(); err != nil {
		t.Fatalf("GetFstabEntries should read /etc/fstab: %v", err)
	}
}

// --- sudo helpers error paths ----------------------------------------------

func TestSudoRun_PasswordRequired(t *testing.T) {
	f := &fakeSys{respond: func(name string, args []string, _ string) (string, error) {
		return "sudo: a password is required", fmt.Errorf("exit 1")
	}}
	defer f.install()()
	err := sudoRun("umount", "/x")
	if err == nil || !strings.Contains(err.Error(), "sudo requires a password") {
		t.Fatalf("err = %v", err)
	}
}

func TestSudoRun_GenericError(t *testing.T) {
	f := &fakeSys{respond: func(name string, args []string, _ string) (string, error) {
		return "umount: not mounted", fmt.Errorf("exit 1")
	}}
	defer f.install()()
	err := sudoRun("umount", "/x")
	if err == nil || !strings.Contains(err.Error(), "not mounted") {
		t.Fatalf("err = %v", err)
	}
}

func TestSudoWrite_PasswordRequired(t *testing.T) {
	f := &fakeSys{respond: func(name string, args []string, _ string) (string, error) {
		return "sudo: password is required", fmt.Errorf("exit 1")
	}}
	defer f.install()()
	err := sudoWrite("/etc/x", "data")
	if err == nil || !strings.Contains(err.Error(), "sudo requires a password") {
		t.Fatalf("err = %v", err)
	}
}

func TestSudoWrite_GenericError(t *testing.T) {
	f := &fakeSys{respond: func(name string, args []string, _ string) (string, error) {
		return "permission denied", fmt.Errorf("exit 1")
	}}
	defer f.install()()
	err := sudoWrite("/etc/x", "data")
	if err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("err = %v", err)
	}
}

// --- CreateAutomount: guest path + error branches --------------------------

func TestCreateAutomount_GuestPath(t *testing.T) {
	f := &fakeSys{}
	f.respond = func(name string, args []string, stdin string) (string, error) {
		if name == "systemd-escape" {
			return "mnt-smb-h-s", nil
		}
		return mountResponder("3.0")(name, args, stdin)
	}
	defer f.install()()

	err := CreateAutomount(AutomountConfig{
		Share:      Share{Host: "h", Name: "s", Path: "//h/s"},
		MountPoint: "/mnt/smb/h/s",
		Options:    MountOptions{UID: "1000", GID: "1000", FileMode: "0755", DirMode: "0755"},
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	unit, ok := f.stdinForTee(".mount")
	if !ok || !strings.Contains(unit, "guest") || !strings.Contains(unit, "sec=none") {
		t.Errorf("guest unit malformed: %q", unit)
	}
	// No credential file should be written for a guest mount.
	if _, ok := f.stdinForTee("/etc/samba/credentials/"); ok {
		t.Error("guest mount must not write a credentials file")
	}
}

func TestCreateAutomount_CredWriteFails(t *testing.T) {
	f := &fakeSys{respond: func(name string, args []string, _ string) (string, error) {
		if name == "systemd-escape" {
			return "mnt-smb-h-s", nil
		}
		if name == "sudo" && len(args) >= 2 && args[1] == "tee" {
			return "denied", fmt.Errorf("exit 1")
		}
		return "", nil
	}}
	defer f.install()()
	err := CreateAutomount(AutomountConfig{
		Share: Share{Host: "h", Name: "s", Path: "//h/s"}, MountPoint: "/mnt/smb/h/s",
		Options: MountOptions{Creds: Credentials{Username: "u", Password: "p"}},
	})
	if err == nil || !strings.Contains(err.Error(), "failed to write credentials") {
		t.Fatalf("err = %v", err)
	}
}

func TestCreateAutomount_WriteMountUnitFails(t *testing.T) {
	f := &fakeSys{}
	f.respond = func(name string, args []string, stdin string) (string, error) {
		if name == "systemd-escape" {
			return "mnt-smb-h-s", nil
		}
		// Guest mount (no cred tee); fail only the unit-file tee.
		if name == "sudo" && len(args) >= 3 && args[1] == "tee" && strings.HasSuffix(args[2], ".mount") {
			return "ro fs", fmt.Errorf("exit 1")
		}
		return mountResponder("2.0")(name, args, stdin)
	}
	defer f.install()()
	err := CreateAutomount(AutomountConfig{
		Share: Share{Host: "h", Name: "s", Path: "//h/s"}, MountPoint: "/mnt/smb/h/s",
		Options: MountOptions{UID: "1000", GID: "1000", FileMode: "0755", DirMode: "0755"},
	})
	if err == nil || !strings.Contains(err.Error(), "failed to write mount unit") {
		t.Fatalf("err = %v", err)
	}
}

func TestCreateAutomount_MkdirFails(t *testing.T) {
	f := &fakeSys{respond: func(name string, args []string, _ string) (string, error) {
		if name == "systemd-escape" {
			return "mnt-smb-h-s", nil
		}
		if name == "sudo" && len(args) >= 2 && args[1] == "mkdir" {
			return "denied", fmt.Errorf("exit 1")
		}
		return "", nil
	}}
	defer f.install()()
	err := CreateAutomount(AutomountConfig{Share: Share{Path: "//h/s"}, MountPoint: "/mnt/smb/h/s"})
	if err == nil || !strings.Contains(err.Error(), "failed to create mount point") {
		t.Fatalf("err = %v", err)
	}
}

func TestCreateAutomount_EnableFails(t *testing.T) {
	f := &fakeSys{}
	f.respond = func(name string, args []string, stdin string) (string, error) {
		if name == "systemd-escape" {
			return "mnt-smb-h-s", nil
		}
		if name == "sudo" && len(args) >= 3 && args[1] == "systemctl" && args[2] == "enable" {
			return "enable boom", fmt.Errorf("exit 1")
		}
		return mountResponder("2.0")(name, args, stdin)
	}
	defer f.install()()
	err := CreateAutomount(AutomountConfig{
		Share: Share{Host: "h", Name: "s", Path: "//h/s"}, MountPoint: "/mnt/smb/h/s",
		Options: MountOptions{UID: "1000", GID: "1000", FileMode: "0755", DirMode: "0755"},
	})
	if err == nil || !strings.Contains(err.Error(), "enable failed") {
		t.Fatalf("err = %v", err)
	}
}

func TestCreateAutomount_StartFails(t *testing.T) {
	f := &fakeSys{}
	f.respond = func(name string, args []string, stdin string) (string, error) {
		if name == "systemd-escape" {
			return "mnt-smb-h-s", nil
		}
		if name == "sudo" && len(args) >= 4 && args[1] == "systemctl" && args[2] == "start" && strings.HasSuffix(args[3], ".automount") {
			return "start boom", fmt.Errorf("exit 1")
		}
		return mountResponder("2.0")(name, args, stdin)
	}
	defer f.install()()
	err := CreateAutomount(AutomountConfig{
		Share: Share{Host: "h", Name: "s", Path: "//h/s"}, MountPoint: "/mnt/smb/h/s",
		Options: MountOptions{UID: "1000", GID: "1000", FileMode: "0755", DirMode: "0755"},
	})
	if err == nil || !strings.Contains(err.Error(), "start failed") {
		t.Fatalf("err = %v", err)
	}
}

func TestCreateAutomount_DaemonReloadFails(t *testing.T) {
	f := &fakeSys{}
	f.respond = func(name string, args []string, stdin string) (string, error) {
		if name == "systemd-escape" {
			return "mnt-smb-h-s", nil
		}
		if name == "sudo" && len(args) >= 3 && args[1] == "systemctl" && args[2] == "daemon-reload" {
			return "reexec failed", fmt.Errorf("exit 1")
		}
		return mountResponder("2.0")(name, args, stdin)
	}
	defer f.install()()
	err := CreateAutomount(AutomountConfig{
		Share:      Share{Host: "h", Name: "s", Path: "//h/s"},
		MountPoint: "/mnt/smb/h/s",
		Options:    MountOptions{UID: "1000", GID: "1000", FileMode: "0755", DirMode: "0755"},
	})
	if err == nil || !strings.Contains(err.Error(), "daemon-reload failed") {
		t.Fatalf("err = %v", err)
	}
}
