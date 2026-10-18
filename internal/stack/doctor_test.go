package stack

import (
	"encoding/pem"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vojislavd/rig/internal/ca"
)

// doctorStack is a linked site "foo" with every outside check faked as
// healthy: port redirect active, CA trusted, resolver and HTTPS fine,
// PHP socket present.
func doctorStack(t *testing.T) *Stack {
	t.Helper()
	st, dir := linkedStack(t)
	os.WriteFile(filepath.Join(dir, "public", "index.php"), []byte("<?php\n"), 0o644)

	authority, err := ca.Load(st.L.CA)
	if err != nil {
		t.Fatal(err)
	}
	anchor := filepath.Join(t.TempDir(), "rig-ca.crt")
	os.WriteFile(anchor, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: authority.Cert.Raw}), 0o644)
	fakeSystem(t, "active", anchor)

	oldLookup, oldTest, oldHTTPS, oldBins := lookupHost, nginxTest, httpsCheck, doctorBinaries
	lookupHost = func(string) ([]string, error) { return []string{"127.0.0.1"}, nil }
	nginxTest = func(string, string) error { return nil }
	httpsCheck = func(string) error { return nil }
	doctorBinaries = doctorBinaries[:0:0] // empty, without touching the real list
	t.Cleanup(func() { lookupHost, nginxTest, httpsCheck, doctorBinaries = oldLookup, oldTest, oldHTTPS, oldBins })

	// A real unix socket where PHP-FPM's would be.
	sock := filepath.Join(st.L.Runtime, "php85", "fpm.sock")
	os.MkdirAll(filepath.Dir(sock), 0o700)
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	return st
}

func problems(checks []Check) map[string]Check {
	out := map[string]Check{}
	for _, c := range checks {
		if c.Status != OK {
			out[c.Name] = c
		}
	}
	return out
}

func TestDoctorHealthy(t *testing.T) {
	st := doctorStack(t)
	checks := st.Doctor(func() error { return nil })
	if p := problems(checks); len(p) > 0 {
		t.Fatalf("problems on a healthy stack: %+v", p)
	}
	areas := map[string]bool{}
	for _, c := range checks {
		areas[c.Area] = true
	}
	for _, a := range []string{"system", "services", "files", "sites"} {
		if !areas[a] {
			t.Errorf("no %s checks", a)
		}
	}
}

func TestDoctorFindsProblems(t *testing.T) {
	st := doctorStack(t)
	lookupHost = func(string) ([]string, error) { return []string{"127.0.0.1", "10.0.0.5"}, nil }

	// A hand edit, a leftover config and a stray unit.
	os.WriteFile(st.confPath("foo"), []byte("# edited\n"), 0o600)
	os.WriteFile(st.confPath("old"), []byte("server {}\n"), 0o600)
	os.WriteFile(filepath.Join(st.L.Units, "rig-old-queue.service"), nil, 0o644)
	// No front controller in the project any more.
	os.Remove(filepath.Join(st.L.Code, "foo", "public", "index.php"))

	p := problems(st.Doctor(nil))
	want := map[string]string{
		"*.localhost resolves":       Fail,
		"MariaDB login":              Fail,
		st.short(st.confPath("foo")): Warn,
		st.short(st.confPath("old")): Warn,
		st.short(filepath.Join(st.L.Units, "rig-old-queue.service")): Warn,
		"foo": Fail,
	}
	for name, status := range want {
		if c, ok := p[name]; !ok || c.Status != status {
			t.Errorf("%s: got %+v, want %s", name, c, status)
		}
	}
	if c := p[st.short(st.confPath("foo"))]; c.Fix != "rig doctor --fix" {
		t.Errorf("drift fix = %q", c.Fix)
	}
	if len(p) != len(want) {
		t.Errorf("unexpected problems: %+v", p)
	}
}

func TestDoctorUntrustedAndDown(t *testing.T) {
	st := doctorStack(t)
	other, _ := ca.New(st.Now())
	anchor := filepath.Join(t.TempDir(), "other.crt")
	os.WriteFile(anchor, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: other.Cert.Raw}), 0o644)
	fakeSystem(t, "inactive", anchor)

	p := problems(st.Doctor(func() error { return nil }))
	for _, name := range []string{"port redirect", "CA trusted"} {
		if p[name].Status != Fail || p[name].Fix != "rig install" {
			t.Errorf("%s = %+v", name, p[name])
		}
	}
}

func TestRepair(t *testing.T) {
	st := doctorStack(t)
	sd := st.S.(*fakeSystemd)

	// Nothing to do on a healthy stack: no rewrite, no reload.
	sd.calls = nil
	changes, err := st.Repair()
	if err != nil || len(changes) != 0 {
		t.Fatalf("Repair on healthy stack = %v, %v", changes, err)
	}
	for _, c := range sd.calls {
		if strings.HasPrefix(c, "reload") {
			t.Error("reloaded nginx with nothing changed")
		}
	}

	// A hand edit and a missing certificate are put back.
	os.WriteFile(st.confPath("foo"), []byte("# edited\n"), 0o600)
	os.Remove(filepath.Join(st.L.Certs, "foo.crt"))
	changes, err = st.Repair()
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(changes, "\n")
	for _, want := range []string{"renewed certificate for foo (no certificate)", "rewrote " + st.short(st.confPath("foo")), "reloaded nginx"} {
		if !strings.Contains(got, want) {
			t.Errorf("changes missing %q:\n%s", want, got)
		}
	}
	if p := problems(st.Doctor(func() error { return nil })); len(p) > 0 {
		t.Errorf("problems after Repair: %+v", p)
	}

	// A leftover config is reported, never deleted.
	os.WriteFile(st.confPath("old"), []byte("server {}\n"), 0o600)
	if _, err := st.Repair(); err != nil {
		t.Fatal(err)
	}
	if !fileExists(st.confPath("old")) {
		t.Error("Repair deleted a file it does not own")
	}
}
