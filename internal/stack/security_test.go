package stack

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseProcAddr(t *testing.T) {
	// Lines copied from a real /proc/net/tcp and tcp6.
	for in, want := range map[string]string{
		"0100007F:1F90":                         "127.0.0.1:8080",
		"010011AC:0035":                         "172.17.0.1:53",
		"00000000:0CEA":                         "0.0.0.0:3306",
		"00000000000000000000000001000000:0277": "::1:631",
		"00000000000000000000000000000000:20FB": ":::8443",
	} {
		ip, port, err := parseProcAddr(in)
		if err != nil {
			t.Errorf("%s: %v", in, err)
			continue
		}
		if got := fmt.Sprintf("%s:%d", ip, port); got != want {
			t.Errorf("%s = %s, want %s", in, got, want)
		}
	}
	for _, bad := range []string{"", "0100007F", "zz00007F:1F90", "0100007F:XYZ", "01007F:1F90"} {
		if _, _, err := parseProcAddr(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

// securityStack is doctorStack with the system-wide inputs of Security
// pointed at temp files describing a clean machine.
func securityStack(t *testing.T) *Stack {
	t.Helper()
	st := doctorStack(t)
	dir := t.TempDir()
	tcp := filepath.Join(dir, "tcp")
	writeProc(t, tcp, fmt.Sprintf("0100007F:20FB %d", os.Getuid()))

	oldProc, oldEnabled, oldPkgs, oldFiles, oldLegacy := procNetTCP, systemEnabled, foreignPackages, installedFiles, legacy
	procNetTCP = []string{tcp, filepath.Join(dir, "tcp6-missing")}
	systemEnabled = func(string) string { return "disabled" }
	foreignPackages = func() ([]string, error) { return []string{"mailpit", "dma"}, nil }
	installedFiles = nil
	legacy.hosts = filepath.Join(dir, "hosts")
	legacy.sitesEnabled = filepath.Join(dir, "sites-enabled")
	legacy.selfSigned = filepath.Join(dir, "self-signed-cert")
	legacy.minio = filepath.Join(dir, "minio-data")
	os.WriteFile(legacy.hosts, []byte("127.0.0.1 localhost\n"), 0o644)
	fakeSystem(t, "inactive", trustAnchor) // old system services are off
	t.Cleanup(func() {
		procNetTCP, systemEnabled, foreignPackages, installedFiles, legacy = oldProc, oldEnabled, oldPkgs, oldFiles, oldLegacy
	})

	os.Chmod(st.L.Code, 0o700)
	os.Chmod(filepath.Dir(st.L.Certs), 0o700)
	os.WriteFile(filepath.Join(st.L.Code, "foo", ".env"), []byte("APP_KEY=x\n"), 0o600)
	return st
}

// writeProc writes a fake /proc/net/tcp; each entry is "addr uid".
func writeProc(t *testing.T, path string, entries ...string) {
	t.Helper()
	var b strings.Builder
	b.WriteString("  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n")
	for i, e := range entries {
		addr, uid, _ := strings.Cut(e, " ")
		fmt.Fprintf(&b, "%4d: %s 00000000:0000 0A 00000000:00000000 00:00000000 00000000 %5s 0 1%d\n", i, addr, uid, i)
	}
	// A connected (not listening) socket on a wildcard address is ignored.
	b.WriteString(" 99: 00000000:0CEA 0100007F:9999 01 00000000:00000000 00:00000000 00000000  1000 0 9\n")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSecurityClean(t *testing.T) {
	st := securityStack(t)
	checks := st.Security()
	var notes []string
	for _, c := range checks {
		switch c.Status {
		case Info:
			notes = append(notes, c.Name)
		case OK:
		default:
			t.Errorf("problem on a clean machine: %+v", c)
		}
	}
	if strings.Join(notes, ",") != "package mailpit" {
		t.Errorf("notes = %v, want only mailpit (dma is not part of the stack)", notes)
	}
}

func TestSecurityFindsProblems(t *testing.T) {
	st := securityStack(t)
	me := fmt.Sprint(os.Getuid())
	writeProc(t, procNetTCP[0],
		"00000000:0CEA 963", // MariaDB on 0.0.0.0, another user
		"010011AC:1771 "+me, // a dev server of yours on 172.17.0.1:6001 (a Reverb port)
		"0500000A:1F40 "+me, // yours on 10.0.0.5:8000, not a rig port
		"010011AC:0035 975", // someone else's DNS: not ours, not rig's
		"0100007F:20FB "+me) // nginx on loopback: fine
	os.Chmod(filepath.Join(st.L.CA, "ca.key"), 0o644)
	os.Chmod(filepath.Join(st.L.Code, "foo", ".env"), 0o644)
	os.WriteFile(filepath.Join(st.L.Code, "foo", "public", "evil.txt"), nil, 0o600)
	os.Chmod(filepath.Join(st.L.Code, "foo", "public", "evil.txt"), 0o666)
	os.Chmod(filepath.Join(st.L.Units, "rig-nginx.service"), 0o666)
	os.WriteFile(legacy.hosts, []byte("127.0.0.1 localhost\n127.0.0.1 shop.test www.shop.test # old\n# 127.0.0.1 gone.test\n"), 0o644)
	os.Mkdir(legacy.sitesEnabled, 0o755)
	os.WriteFile(filepath.Join(legacy.sitesEnabled, "shop.test"), nil, 0o644)
	os.Mkdir(legacy.minio, 0o700)
	systemEnabled = func(u string) string {
		if u == "nginx.service" {
			return "enabled"
		}
		return "disabled"
	}

	got := map[string]Check{}
	for _, c := range st.Security() {
		if c.Status != OK && c.Status != Info {
			got[c.Name] = c
		}
	}
	want := map[string]string{
		"listener 0.0.0.0:3306":                                  Fail,
		"listener 172.17.0.1:6001":                               Fail,
		"listener 10.0.0.5:8000":                                 Warn,
		st.short(filepath.Join(st.L.CA, "ca.key")):               Fail,
		st.short(filepath.Join(st.L.Code, "foo", ".env")):        Warn,
		st.short(filepath.Join(st.L.Units, "rig-nginx.service")): Fail,
		"world-writable in " + st.short(st.L.Code):               Fail,
		"system nginx.service":                                   Warn,
		legacy.hosts:                                             Warn,
		legacy.sitesEnabled:                                      Warn,
		st.short(legacy.minio):                                   Warn,
	}
	for name, status := range want {
		if c, ok := got[name]; !ok || c.Status != status {
			t.Errorf("%s: got %+v, want %s", name, c, status)
		}
	}
	if len(got) != len(want) {
		for name, c := range got {
			if _, ok := want[name]; !ok {
				t.Errorf("unexpected: %+v", c)
			}
		}
	}
	if c := got[st.short(filepath.Join(st.L.CA, "ca.key"))]; !strings.Contains(c.Fix, "rig ca rotate") {
		t.Errorf("CA key fix = %q", c.Fix)
	}
	if c := got[legacy.hosts]; c.Detail != "2 old .test names" {
		t.Errorf("hosts detail = %q (comments must not count)", c.Detail)
	}
	if c := got["world-writable in "+st.short(st.L.Code)]; !strings.HasPrefix(c.Detail, "1: ") {
		t.Errorf("world-writable detail = %q", c.Detail)
	}
}

func TestSecurityRootFiles(t *testing.T) {
	st := securityStack(t)
	// A file owned by you where rig install left a root-owned one.
	f := filepath.Join(t.TempDir(), "rig-ca.crt")
	os.WriteFile(f, nil, 0o644)
	installedFiles = []string{f, "/nonexistent/rig"}
	for _, c := range st.Security() {
		if c.Name == f {
			if c.Status != Fail || !strings.Contains(c.Detail, "not root") {
				t.Errorf("root file check = %+v", c)
			}
			return
		}
	}
	t.Error("installed file not checked")
}
