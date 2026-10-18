package stack

import (
	"bufio"
	"encoding/hex"
	"fmt"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"

	"github.com/vojislavd/rig/internal/fsutil"
	"github.com/vojislavd/rig/internal/garage"
	"github.com/vojislavd/rig/internal/install"
	"github.com/vojislavd/rig/internal/mailpit"
	"github.com/vojislavd/rig/internal/nginx"
	"github.com/vojislavd/rig/internal/site"
)

// Info is a fact worth seeing that needs no action (an accepted risk).
const Info = "info"

// These read the system; tests replace them.
var (
	// procNetTCP lists every TCP socket on the machine with its owner.
	// Reading it needs no privileges.
	procNetTCP = []string{"/proc/net/tcp", "/proc/net/tcp6"}

	systemEnabled = func(unit string) string {
		out, _ := exec.Command("/usr/bin/systemctl", "is-enabled", unit).Output()
		return strings.TrimSpace(string(out))
	}

	// foreignPackages lists installed packages that are not from Arch's
	// official repositories (AUR or built by hand).
	foreignPackages = func() ([]string, error) {
		out, err := exec.Command("/usr/bin/pacman", "-Qmq").Output()
		return strings.Fields(string(out)), err
	}

	// installedFiles are the root-owned files rig install wrote.
	installedFiles = install.InstalledFiles

	// legacy are leftovers of the setup before rig.
	legacy = struct{ hosts, sitesEnabled, selfSigned, minio string }{
		"/etc/hosts", "/etc/nginx/sites-enabled", "/etc/ssl/self-signed-cert", "~/minio-data",
	}
)

// stackPackages are the packages rig's services come from.
var stackPackages = []string{"nginx", "php", "php-fpm", "mariadb", "mailpit", "garage"}

// Security runs the "rig doctor --security" checks.
// Like Doctor, it only reads.
func (st *Stack) Security() []Check {
	var out []Check
	add := func(name, status, detail, fix string) {
		out = append(out, Check{Area: "security", Name: name, Status: status, Detail: detail, Fix: fix})
	}

	// --- Listeners: rig's ports must be on loopback only, whoever owns
	// them. Anything else of yours listening on the network is shown too:
	// on a laptop that joins other Wi-Fi networks, nothing should.
	listeners, err := readListeners()
	if err != nil {
		add("listeners", Warn, "can't read "+strings.Join(procNetTCP, ", ")+": "+err.Error(), "")
	} else {
		bad := 0
		for _, l := range listeners {
			if l.IP.IsLoopback() {
				continue
			}
			addr := net.JoinHostPort(l.IP.String(), strconv.Itoa(l.Port))
			switch {
			case rigPort(l.Port):
				bad++
				add("listener "+addr, Fail, fmt.Sprintf("a rig port reachable from the network (uid %d)", l.UID), "make it listen on 127.0.0.1 only")
			case l.UID == os.Getuid():
				bad++
				add("listener "+addr, Warn, "one of your programs is reachable from the network", "stop it, or make it listen on 127.0.0.1")
			}
		}
		if bad == 0 {
			add("listeners", OK, "rig ports on loopback only", "")
		}
	}

	// --- Secrets: must be yours and private (no group/other access).
	// A loose mode may mean someone else already read the file.
	dataDir := filepath.Dir(st.L.Certs)
	secrets := []struct {
		path string
		dir  bool
		fix  string
	}{
		{st.L.Config, true, ""},
		{st.L.Registry(), false, ""},
		{filepath.Join(st.L.Config, "mariadb.cnf"), false, "chmod 600 it, then change rigadmin's password (rig install)"},
		{st.garageConf(), false, ""},
		{filepath.Join(st.L.Config, "nginx"), true, ""},
		{filepath.Join(st.L.Config, "php"), true, ""},
		{dataDir, true, ""},
		{st.L.CA, true, "chmod 700 it; if anyone else could read the key, rotate the CA (rig ca rotate)"},
		{filepath.Join(st.L.CA, "ca.key"), false, "chmod 600 it; if anyone else could read it, rotate the CA (rig ca rotate)"},
		{st.L.Certs, true, ""},
		{filepath.Join(dataDir, "backups"), true, ""},
		{st.garageData(), true, ""},
		{st.mailDir(), true, ""},
	}
	for _, pat := range []string{filepath.Join(st.L.Certs, "*.key"), filepath.Join(dataDir, "backups", "*")} {
		matches, _ := filepath.Glob(pat)
		for _, m := range matches {
			secrets = append(secrets, struct {
				path string
				dir  bool
				fix  string
			}{m, false, ""})
		}
	}
	for _, s := range secrets {
		if _, err := os.Lstat(s.path); err != nil {
			continue // not created yet; Doctor reports what must exist
		}
		if err := fsutil.CheckPrivate(s.path, s.dir); err != nil {
			add(st.short(s.path), Fail, err.Error(), s.fix)
		} else {
			add(st.short(s.path), OK, "private", "")
		}
	}

	// --- Files that decide what runs or what is trusted: anyone who can
	// write them can run code as you (units, the rig binary) or as root
	// (the nat unit), or make the system trust another CA.
	var yours []string
	if self, err := os.Executable(); err == nil {
		yours = append(yours, self)
	}
	yours = append(yours, st.L.Units, filepath.Join(st.L.CA, "ca.crt"))
	units, _ := filepath.Glob(filepath.Join(st.L.Units, "rig-*.service"))
	crts, _ := filepath.Glob(filepath.Join(st.L.Certs, "*.crt"))
	yours = append(append(yours, units...), crts...)
	for _, p := range yours {
		checkOwner(add, st.short(p), p, os.Getuid())
	}
	for _, p := range installedFiles {
		checkOwner(add, p, p, 0)
	}

	// --- Projects.
	if err := fsutil.CheckPrivate(st.L.Code, true); err != nil {
		add(st.short(st.L.Code), Warn, err.Error(), "chmod 700 "+st.short(st.L.Code))
	}
	if r, err := site.Load(st.L.Registry()); err == nil {
		for _, s := range r.List() {
			p := filepath.Join(s.Dir, ".env")
			info, err := os.Lstat(p)
			if err != nil {
				continue
			}
			switch {
			case !info.Mode().IsRegular():
				add(st.short(p), Fail, "not a regular file (symlink?)", "")
			case owner(info) != os.Getuid():
				add(st.short(p), Fail, fmt.Sprintf("owned by uid %d, not you", owner(info)), "")
			case info.Mode().Perm()&0o022 != 0:
				add(st.short(p), Fail, fmt.Sprintf("mode %04o: others can change it", info.Mode().Perm()), "chmod 600 "+st.short(p))
			case info.Mode().Perm()&0o044 != 0:
				add(st.short(p), Warn, fmt.Sprintf("mode %04o: others can read its secrets", info.Mode().Perm()), "chmod 600 "+st.short(p))
			default:
				add(st.short(p), OK, "private", "")
			}
		}
	}
	if found, n := worldWritable(st.L.Code, 5); n > 0 {
		add("world-writable in "+st.short(st.L.Code), Fail, fmt.Sprintf("%d: %s", n, strings.Join(found, ", ")),
			"find "+st.short(st.L.Code)+" -perm -o+w ! -type l -exec chmod o-w {} +")
	} else {
		add("world-writable in "+st.short(st.L.Code), OK, "none", "")
	}

	// --- Leftovers of the old setup.
	for _, u := range []string{"nginx.service", "php-fpm.service"} {
		if systemEnabled(u) == "enabled" || systemState(u) == "active" {
			add("system "+u, Warn, "the old system service is still on; rig runs its own", "sudo systemctl disable --now "+u)
		}
	}
	if entries, err := os.ReadDir(legacy.sitesEnabled); err == nil && len(entries) > 0 {
		add(legacy.sitesEnabled, Warn, fmt.Sprintf("%d old vhosts", len(entries)), "delete them")
	}
	if data, err := os.ReadFile(legacy.hosts); err == nil {
		n := 0
		sc := bufio.NewScanner(strings.NewReader(string(data)))
		for sc.Scan() {
			// "IP name name ... # comment": count names, skip comments.
			line, _, _ := strings.Cut(sc.Text(), "#")
			fields := strings.Fields(line)
			for i := 1; i < len(fields); i++ {
				if strings.HasSuffix(fields[i], ".test") {
					n++
				}
			}
		}
		if n > 0 {
			add(legacy.hosts, Warn, fmt.Sprintf("%d old .test names", n), "remove the .test lines (sudo)")
		}
	}
	for _, p := range []string{legacy.selfSigned, expandHome(legacy.minio)} {
		if _, err := os.Lstat(p); err == nil {
			add(st.short(p), Warn, "left over from the old setup", "delete it")
		}
	}

	// --- Where the stack comes from. AUR packages are built on this
	// machine from someone else's recipe, not signed by Arch.
	// Accepted, but worth knowing.
	if pkgs, err := foreignPackages(); err == nil {
		for _, p := range stackPackages {
			if slices.Contains(pkgs, p) {
				add("package "+p, Info, "from the AUR, not signed by Arch", "")
			}
		}
	}
	return out
}

// checkOwner reports a file that is not owned by uid, is a symlink, or
// can be written by group or others.
func checkOwner(add func(name, status, detail, fix string), name, path string, uid int) {
	info, err := os.Lstat(path)
	if err != nil {
		return
	}
	who := "you"
	if uid == 0 {
		who = "root"
	}
	switch {
	case info.Mode()&fs.ModeSymlink != 0:
		add(name, Fail, "is a symlink", "")
	case owner(info) != uid:
		add(name, Fail, fmt.Sprintf("owned by uid %d, not %s", owner(info), who), "")
	case info.Mode().Perm()&0o022 != 0:
		add(name, Fail, fmt.Sprintf("mode %04o: others can change it", info.Mode().Perm()), "chmod go-w "+path)
	default:
		add(name, OK, "only "+who+" can change it", "")
	}
}

func owner(info fs.FileInfo) int {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return int(st.Uid)
	}
	return -1
}

// worldWritable walks root (never following symlinks) and returns up to
// max world-writable paths plus the total count. Symlinks themselves
// always show mode 0777 and are skipped.
func worldWritable(root string, max int) ([]string, int) {
	var found []string
	n := 0
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		info, err := d.Info()
		if err == nil && info.Mode().Perm()&0o002 != 0 {
			n++
			if len(found) < max {
				found = append(found, p)
			}
		}
		return nil
	})
	return found, n
}

func expandHome(p string) string {
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, rest)
		}
	}
	return p
}

// rigPort reports whether port is one rig's stack listens on.
func rigPort(port int) bool {
	if port >= site.MinReverbPort && port <= site.MaxReverbPort {
		return true
	}
	ports := []int{nginx.HTTPPort, nginx.HTTPSPort, 3306} // 3306: MariaDB
	for _, a := range []string{mailpit.SMTPAddr, mailpit.UIAddr, garage.S3Addr, garage.RPCAddr, garage.AdminAddr} {
		if _, p, err := net.SplitHostPort(a); err == nil {
			n, _ := strconv.Atoi(p)
			ports = append(ports, n)
		}
	}
	return slices.Contains(ports, port)
}

// listener is one listening TCP socket.
type listener struct {
	IP   net.IP
	Port int
	UID  int
}

// readListeners parses /proc/net/tcp and tcp6. Each line looks like
//
//	0: 0100007F:1F90 00000000:0000 0A ... 1000 ...
//
// with the local address in hex, the state ("0A" = listening) in the 4th
// field and the owner's uid in the 8th.
func readListeners() ([]listener, error) {
	var out []listener
	for _, path := range procNetTCP {
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue // e.g. IPv6 turned off
			}
			return nil, err
		}
		for _, line := range strings.Split(string(data), "\n")[1:] {
			f := strings.Fields(line)
			if len(f) < 8 || f[3] != "0A" {
				continue
			}
			ip, port, err := parseProcAddr(f[1])
			if err != nil {
				return nil, fmt.Errorf("%s: %w", path, err)
			}
			uid, err := strconv.Atoi(f[7])
			if err != nil {
				return nil, fmt.Errorf("%s: bad uid %q", path, f[7])
			}
			out = append(out, listener{ip, port, uid})
		}
	}
	return out, nil
}

// parseProcAddr decodes "0100007F:1F90" into 127.0.0.1 and 8080. The
// kernel prints the address as 32-bit words in this machine's byte order
// (little-endian on x86 and ARM), so each 4-byte group is reversed.
func parseProcAddr(s string) (net.IP, int, error) {
	hexIP, hexPort, ok := strings.Cut(s, ":")
	raw, err := hex.DecodeString(hexIP)
	if !ok || err != nil || (len(raw) != 4 && len(raw) != 16) {
		return nil, 0, fmt.Errorf("bad address %q", s)
	}
	for i := 0; i < len(raw); i += 4 {
		raw[i], raw[i+1], raw[i+2], raw[i+3] = raw[i+3], raw[i+2], raw[i+1], raw[i]
	}
	port, err := strconv.ParseUint(hexPort, 16, 16)
	if err != nil {
		return nil, 0, fmt.Errorf("bad port in %q", s)
	}
	return net.IP(raw), int(port), nil
}
