package stack

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/vojislavd/rig/internal/ca"
	"github.com/vojislavd/rig/internal/garage"
	"github.com/vojislavd/rig/internal/install"
	"github.com/vojislavd/rig/internal/mailpit"
	"github.com/vojislavd/rig/internal/nginx"
	"github.com/vojislavd/rig/internal/php"
	"github.com/vojislavd/rig/internal/site"
	"github.com/vojislavd/rig/internal/worker"
)

// Check is one line of "rig doctor".
type Check struct {
	Area   string `json:"area"`   // system, services, files, sites, security
	Name   string `json:"name"`   // what was checked
	Status string `json:"status"` // ok, info, warn, fail
	Detail string `json:"detail,omitempty"`
	Fix    string `json:"fix,omitempty"` // what to do about it
}

const (
	OK   = "ok"
	Warn = "warn"
	Fail = "fail"
)

// caWarnBefore is how early doctor warns that the CA itself expires.
const caWarnBefore = 365 * 24 * time.Hour

// These run outside programs or open connections; tests replace them.
var (
	// lookupHost resolves a name through the system resolver (glibc, the
	// same path PHP and curl use), not Go's own resolver.
	lookupHost = func(host string) ([]string, error) {
		out, err := exec.Command("/usr/bin/getent", "ahosts", host).Output()
		if err != nil {
			return nil, fmt.Errorf("getent: %w", err)
		}
		var addrs []string
		for _, line := range strings.Split(string(out), "\n") {
			if f := strings.Fields(line); len(f) > 0 && !slices.Contains(addrs, f[0]) {
				addrs = append(addrs, f[0])
			}
		}
		return addrs, nil
	}

	// nginxTest runs "nginx -t" on rig's config, as the user.
	nginxTest = func(conf, startupLog string) error {
		out, err := exec.Command("/usr/bin/nginx", "-t", "-q", "-e", startupLog, "-c", conf).CombinedOutput()
		if err != nil {
			return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
		}
		return nil
	}

	// httpsCheck connects the way a browser does: port 443 (so through
	// the port redirect), with the system trust store verifying the
	// certificate for host. Nothing is sent; the connection is closed
	// after the handshake.
	httpsCheck = func(host string) error {
		d := &net.Dialer{Timeout: 2 * time.Second}
		conn, err := tls.DialWithDialer(d, "tcp", "127.0.0.1:443", &tls.Config{ServerName: host})
		if err != nil {
			return err
		}
		return conn.Close()
	}

	// binaries rig runs, and whether each one is required.
	doctorBinaries = []struct {
		path     string
		required bool
		usedBy   string
	}{
		{"/usr/bin/nginx", true, "web server"},
		{php.Default.Binary, true, "PHP-FPM"},
		{php.Default.CLI, true, "workers"},
		{"/usr/bin/mariadb", true, "databases"},
		{"/usr/bin/mariadb-dump", true, "database backups"},
		{"/usr/bin/mailpit", false, "rig mail"},
		{"/usr/bin/garage", false, "rig s3"},
	}
)

// Doctor runs every health check. dbCheck tries rig's MariaDB login;
// nil means it could not even be set up (shown as a failure).
// Doctor only reads: it never changes a file or a service.
func (st *Stack) Doctor(dbCheck func() error) []Check {
	var out []Check
	add := func(area, name, status, detail, fix string) {
		out = append(out, Check{Area: area, Name: name, Status: status, Detail: detail, Fix: fix})
	}

	// --- system: what rig install set up, and the tools rig runs.
	for _, b := range doctorBinaries {
		info, err := os.Stat(b.path)
		switch {
		case err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0:
			add("system", b.path, OK, "", "")
		case b.required:
			add("system", b.path, Fail, "missing (needed for "+b.usedBy+")", "install it with pacman")
		default:
			add("system", b.path, Warn, "missing ("+b.usedBy+" won't work)", "install it if you need "+b.usedBy)
		}
	}

	if s := systemState(install.NatUnitName); s == "active" {
		add("system", "port redirect", OK, "", "")
	} else {
		add("system", "port redirect", Fail, install.NatUnitName+" is "+s, "rig install")
	}

	authority, caErr := ca.Load(st.L.CA)
	switch {
	case caErr != nil:
		add("system", "CA", Fail, caErr.Error(), "rig install")
	case st.Now().Add(caWarnBefore).After(authority.Cert.NotAfter):
		add("system", "CA", Warn, "expires "+authority.Cert.NotAfter.Format(time.DateOnly), "rotate the CA (rig ca rotate)")
	default:
		add("system", "CA", OK, "valid until "+authority.Cert.NotAfter.Format(time.DateOnly), "")
	}
	if st.caTrusted() {
		add("system", "CA trusted", OK, "", "")
	} else {
		add("system", "CA trusted", Fail, install.TrustAnchorPath+" missing or not rig's current CA", "rig install")
	}

	// A name rig never uses, so the answer comes from the resolver's
	// .localhost rule, not from any site.
	if addrs, err := lookupHost("rig-doctor-check." + site.TLD); err != nil {
		add("system", "*.localhost resolves", Fail, err.Error(), "check systemd-resolved and /etc/nsswitch.conf")
	} else if bad := nonLoopback(addrs); len(addrs) == 0 || len(bad) > 0 {
		add("system", "*.localhost resolves", Fail, "resolves to "+strings.Join(addrs, ", "), "*.localhost must resolve to 127.0.0.1 only")
	} else {
		add("system", "*.localhost resolves", OK, strings.Join(addrs, ", "), "")
	}

	if dbCheck == nil {
		add("system", "MariaDB login", Fail, "no rig login file", "rig install")
	} else if err := dbCheck(); err != nil {
		add("system", "MariaDB login", Fail, err.Error(), "is mariadb running? (systemctl status mariadb)")
	} else {
		add("system", "MariaDB login", OK, "", "")
	}

	// --- services.
	nginxActive := false
	for _, u := range []string{nginx.UnitName, php.Default.UnitName()} {
		if s := st.S.IsActive(u); s == "active" {
			add("services", u, OK, "", "")
			if u == nginx.UnitName {
				nginxActive = true
			}
		} else {
			add("services", u, Fail, s, "journalctl --user -u "+u)
		}
	}
	nd := st.L.nginxDirs()
	if err := nginxTest(filepath.Join(nd.Conf, "nginx.conf"), filepath.Join(nd.Logs, "nginx-startup.log")); err != nil {
		add("services", "nginx -t", Fail, err.Error(), "")
	} else {
		add("services", "nginx -t", OK, "", "")
	}
	sock := php.Socket(st.L.phpDirs(), php.Default)
	if info, err := os.Lstat(sock); err == nil && info.Mode()&os.ModeSocket != 0 {
		add("services", "PHP socket", OK, sock, "")
	} else {
		add("services", "PHP socket", Fail, sock+" missing", "systemctl --user restart "+php.Default.UnitName())
	}
	// On-demand services and workers: only a failure is worth a line.
	onDemand := []string{mailpit.UnitName, garage.UnitName}
	workers, _ := st.Workers()
	for _, w := range workers {
		onDemand = append(onDemand, w.Unit)
	}
	for _, u := range onDemand {
		if fileExists(filepath.Join(st.L.Units, u)) && st.S.IsActive(u) == "failed" {
			add("services", u, Warn, "failed", "journalctl --user -u "+u)
		}
	}

	// --- files: everything rig generates must match what it would write
	// now. A difference means a hand edit, or a rig upgrade with a new
	// template.
	r, regErr := site.Load(st.L.Registry())
	if regErr != nil {
		add("files", "sites.json", Fail, regErr.Error(), "")
		return out
	}
	files, err := st.serviceFiles()
	if err != nil {
		add("files", "service files", Fail, err.Error(), "")
	}
	for _, f := range files {
		add("files", st.short(f.path), fileState(f), fileDetail(f), fixIfDrift(f))
	}
	for _, s := range r.List() {
		conf, err := st.siteConfig(s)
		if err != nil {
			add("files", s.Name+".conf", Fail, err.Error(), "")
			continue
		}
		f := file{nginx.SiteConfigPath(nd.Conf, s.Name), conf, 0o600}
		add("files", st.short(f.path), fileState(f), fileDetail(f), fixIfDrift(f))
	}
	// Leftovers: a config nginx serves, or a rig unit, for no known site.
	if entries, err := os.ReadDir(filepath.Join(nd.Conf, "rig.d")); err == nil {
		for _, e := range entries {
			name, ok := strings.CutSuffix(e.Name(), ".conf")
			if _, known := r.Get(name); !ok || !known {
				add("files", st.short(filepath.Join(nd.Conf, "rig.d", e.Name())), Warn, "not a linked site, but nginx loads it", "delete it, then reload nginx")
			}
		}
	}
	known := map[string]bool{nginx.UnitName: true, php.Default.UnitName(): true, mailpit.UnitName: true, garage.UnitName: true}
	for _, s := range r.List() {
		for _, k := range worker.KindNames() {
			known[worker.UnitName(s.Name, k)] = true
		}
	}
	if entries, err := os.ReadDir(st.L.Units); err == nil {
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), "rig-") && strings.HasSuffix(e.Name(), ".service") && !known[e.Name()] {
				add("files", st.short(filepath.Join(st.L.Units, e.Name())), Warn, "rig unit for no known site", "systemctl --user stop "+e.Name()+", then delete it")
			}
		}
	}

	// --- sites.
	sites, _ := st.Sites()
	for _, info := range sites {
		if _, err := os.Stat(filepath.Join(info.Dir, "public", "index.php")); err != nil {
			add("sites", info.Name, Fail, "no "+filepath.Join(info.Dir, "public", "index.php"), "restore the project, or rig unlink "+info.Name)
			continue
		}
		switch {
		case info.CertIssue != "" && (info.CertExpiry.IsZero() || !st.Now().Before(info.CertExpiry)):
			add("sites", info.Name, Fail, "certificate: "+info.CertIssue, "rig doctor --fix")
		case info.CertIssue != "":
			add("sites", info.Name, Warn, "certificate: "+info.CertIssue, "rig doctor --fix")
		case !nginxActive:
			add("sites", info.Name, Warn, "not checked: nginx is down", "")
		default:
			// End to end: resolver, port redirect, nginx, certificate and
			// trust, exactly as a browser sees them.
			if err := httpsCheck(site.Host(info.Name)); err != nil {
				add("sites", info.Name, Fail, info.URL+": "+err.Error(), "")
			} else {
				add("sites", info.Name, OK, info.URL+" (certificate until "+info.CertExpiry.Format(time.DateOnly)+")", "")
			}
		}
	}
	return out
}

func fixIfDrift(f file) string {
	if fileDetail(f) == "" {
		return ""
	}
	return "rig doctor --fix"
}

// fileState compares a generated file on disk with what rig would write.
func fileState(f file) string {
	if fileDetail(f) == "" {
		return OK
	}
	if _, err := os.Lstat(f.path); err != nil {
		return Fail
	}
	return Warn
}

func fileDetail(f file) string {
	info, err := os.Lstat(f.path)
	if err != nil {
		return "missing"
	}
	if !info.Mode().IsRegular() {
		return "not a regular file"
	}
	if info.Mode().Perm() != f.mode {
		return fmt.Sprintf("mode %04o, want %04o", info.Mode().Perm(), f.mode)
	}
	if old, err := os.ReadFile(f.path); err != nil || !bytes.Equal(old, f.content) {
		return "differs from what rig generates (hand edit, or rig was updated)"
	}
	return ""
}

// short shows a path with ~ for the home folder.
func (st *Stack) short(p string) string {
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(p, home+"/") {
		return "~" + p[len(home):]
	}
	return p
}

// nonLoopback returns the addresses that are not 127.0.0.0/8 or ::1.
func nonLoopback(addrs []string) []string {
	var bad []string
	for _, a := range addrs {
		if ip := net.ParseIP(a); ip == nil || !ip.IsLoopback() {
			bad = append(bad, a)
		}
	}
	return bad
}
