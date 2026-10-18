package stack

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vojislavd/rig/internal/ca"
	"github.com/vojislavd/rig/internal/site"
)

// fakeSystemd records calls; failReload makes every Reload fail.
type fakeSystemd struct {
	calls      []string
	failReload bool
}

func (f *fakeSystemd) DaemonReload() error {
	f.calls = append(f.calls, "daemon-reload")
	return nil
}

func (f *fakeSystemd) EnableNow(units ...string) error {
	f.calls = append(f.calls, "enable --now "+strings.Join(units, " "))
	return nil
}

func (f *fakeSystemd) Start(unit string) error {
	f.calls = append(f.calls, "start "+unit)
	return nil
}

func (f *fakeSystemd) Stop(units ...string) error {
	f.calls = append(f.calls, "stop "+strings.Join(units, " "))
	return nil
}

func (f *fakeSystemd) Restart(unit string) error {
	f.calls = append(f.calls, "restart "+unit)
	return nil
}

func (f *fakeSystemd) IsActive(string) string { return "active" }

func (f *fakeSystemd) Reload(unit string) error {
	f.calls = append(f.calls, "reload "+unit)
	if f.failReload {
		return errors.New("nginx -t failed")
	}
	return nil
}

// testStack builds a Stack in a temp folder with a real CA and one
// Laravel-like project, ~/code/foo with a public/ folder.
func testStack(t *testing.T) (*Stack, *fakeSystemd) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	l := Layout{
		Code:    filepath.Join(root, "code"),
		Config:  filepath.Join(root, "config", "rig"),
		Logs:    filepath.Join(root, "state", "rig", "logs"),
		Runtime: filepath.Join(root, "run", "rig"),
		Certs:   filepath.Join(root, "data", "rig", "certs"),
		CA:      filepath.Join(root, "data", "rig", "ca"),
		Units:   filepath.Join(root, "config", "systemd", "user"),
	}
	if err := os.MkdirAll(filepath.Join(l.Code, "foo", "public"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(l.CA), 0o700); err != nil {
		t.Fatal(err)
	}
	c, err := ca.New(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Save(l.CA); err != nil {
		t.Fatal(err)
	}
	f := &fakeSystemd{}
	return &Stack{L: l, S: f, Now: time.Now}, f
}

func (st *Stack) confPath(name string) string {
	return filepath.Join(st.L.Config, "nginx", "rig.d", name+".conf")
}

func TestLink(t *testing.T) {
	st, sd := testStack(t)
	s, err := st.Link(filepath.Join(st.L.Code, "foo"), "")
	if err != nil {
		t.Fatal(err)
	}
	if s.Name != "foo" || s.Dir != filepath.Join(st.L.Code, "foo") {
		t.Errorf("site = %+v", s)
	}

	r, err := site.Load(st.L.Registry())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := r.Get("foo"); !ok {
		t.Error("foo not in the registry")
	}
	for _, p := range []string{
		st.confPath("foo"),
		filepath.Join(st.L.Certs, "foo.crt"),
		filepath.Join(st.L.Certs, "foo.key"),
		filepath.Join(st.L.Config, "nginx", "nginx.conf"),
		filepath.Join(st.L.Config, "php", "php85.conf"),
		filepath.Join(st.L.Units, "rig-nginx.service"),
		filepath.Join(st.L.Units, "rig-php85.service"),
	} {
		if !fileExists(p) {
			t.Errorf("%s missing", p)
		}
	}
	conf, _ := os.ReadFile(st.confPath("foo"))
	if !strings.Contains(string(conf), "server_name foo.localhost;") {
		t.Error("site config does not serve foo.localhost")
	}
	want := []string{"daemon-reload", "enable --now rig-nginx.service rig-php85.service", "reload rig-nginx.service"}
	if !slices.Equal(sd.calls, want) {
		t.Errorf("systemd calls = %v, want %v", sd.calls, want)
	}
}

func TestLinkTwice(t *testing.T) {
	st, _ := testStack(t)
	dir := filepath.Join(st.L.Code, "foo")
	if _, err := st.Link(dir, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Link(dir, ""); !errors.Is(err, site.ErrExists) {
		t.Errorf("second Link = %v, want ErrExists", err)
	}
}

func TestEnsureServicesIdempotent(t *testing.T) {
	st, sd := testStack(t)
	if changed, err := st.ensureServices(); err != nil || !changed {
		t.Fatalf("first ensureServices = %v, %v; want changed", changed, err)
	}
	sd.calls = nil
	if changed, err := st.ensureServices(); err != nil || changed {
		t.Fatalf("second ensureServices = %v, %v; want unchanged", changed, err)
	}
	// Nothing changed, so no daemon-reload; services are still ensured.
	if slices.Contains(sd.calls, "daemon-reload") {
		t.Errorf("daemon-reload on unchanged files: %v", sd.calls)
	}
}

func TestLinkRollsBackOnFailure(t *testing.T) {
	st, sd := testStack(t)
	sd.failReload = true
	_, err := st.Link(filepath.Join(st.L.Code, "foo"), "")
	if err == nil || !strings.Contains(err.Error(), "nginx -t failed") {
		t.Fatalf("Link = %v, want the nginx reload failure", err)
	}
	if fileExists(st.confPath("foo")) {
		t.Error("site config left behind")
	}
	if fileExists(filepath.Join(st.L.Certs, "foo.crt")) || fileExists(filepath.Join(st.L.Certs, "foo.key")) {
		t.Error("certificate left behind")
	}
	r, err := site.Load(st.L.Registry())
	if err != nil {
		t.Fatal(err)
	}
	if len(r.List()) != 0 {
		t.Error("site registered although Link failed")
	}
}

func TestLinkRejects(t *testing.T) {
	st, _ := testStack(t)
	outside := t.TempDir()
	noPublic := filepath.Join(st.L.Code, "bare")
	if err := os.Mkdir(noPublic, 0o700); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, dir, site, wantErr string
	}{
		{"outside code dir", outside, "", "directly inside"},
		{"no public folder", noPublic, "", "no public/ folder"},
		{"name differs from folder", filepath.Join(st.L.Code, "foo"), "bar", "must be named"},
		{"invalid name", filepath.Join(st.L.Code, "foo"), "Foo", "invalid site name"},
	}
	for _, tt := range tests {
		_, err := st.Link(tt.dir, tt.site)
		if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
			t.Errorf("%s: Link = %v, want error containing %q", tt.name, err, tt.wantErr)
		}
	}
}

func TestLinkWithoutCA(t *testing.T) {
	st, _ := testStack(t)
	if err := os.RemoveAll(st.L.CA); err != nil {
		t.Fatal(err)
	}
	_, err := st.Link(filepath.Join(st.L.Code, "foo"), "")
	if err == nil || !strings.Contains(err.Error(), "rig install") {
		t.Errorf("Link without CA = %v, want hint to run rig install", err)
	}
}

func TestUnlink(t *testing.T) {
	st, sd := testStack(t)
	dir := filepath.Join(st.L.Code, "foo")
	marker := filepath.Join(dir, "public", "index.php")
	if err := os.WriteFile(marker, []byte("<?php"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Link(dir, ""); err != nil {
		t.Fatal(err)
	}
	sd.calls = nil
	if err := st.Unlink("foo"); err != nil {
		t.Fatal(err)
	}
	if fileExists(st.confPath("foo")) || fileExists(filepath.Join(st.L.Certs, "foo.crt")) {
		t.Error("config or certificate left after Unlink")
	}
	if !fileExists(marker) {
		t.Error("Unlink touched the project folder")
	}
	if !slices.Equal(sd.calls, []string{"reload rig-nginx.service"}) {
		t.Errorf("systemd calls = %v", sd.calls)
	}
	if err := st.Unlink("foo"); !errors.Is(err, site.ErrNotFound) {
		t.Errorf("second Unlink = %v, want ErrNotFound", err)
	}
}

func TestSites(t *testing.T) {
	st, _ := testStack(t)
	if _, err := st.Link(filepath.Join(st.L.Code, "foo"), ""); err != nil {
		t.Fatal(err)
	}
	list, err := st.Sites()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].URL != "https://foo.localhost" || list[0].CertIssue != "" {
		t.Errorf("Sites = %+v", list)
	}
	if left := time.Until(list[0].CertExpiry); left < 390*24*time.Hour {
		t.Errorf("cert expires in %v, want about 397 days", left)
	}
}
