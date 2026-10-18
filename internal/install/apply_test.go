package install

import (
	"bytes"
	"errors"
	"io/fs"
	"slices"
	"strings"
	"testing"
)

// fakeSystem records every command instead of running it.
type fakeSystem struct {
	calls    [][]string
	stdins   [][]byte
	existing map[string]bool
	failOn   string // fail any command containing this argument
	verified []string
}

// systemDirs are folders every Arch install has; add extra ones per test.
func systemDirs(extra ...string) map[string]bool {
	m := map[string]bool{
		"/etc/systemd/system":                       true,
		"/etc/ca-certificates/trust-source/anchors": true,
		sudo:                 true,
		"/usr/bin/install":   true,
		"/usr/bin/rm":        true,
		"/usr/bin/rmdir":     true,
		"/usr/bin/systemctl": true,
		"/usr/bin/nft":       true,
		"/usr/bin/mariadb":   true,
		updateCATrust:        true,
	}
	for _, e := range extra {
		m[e] = true
	}
	return m
}

func (f *fakeSystem) applier() *Applier {
	return &Applier{
		Run: func(argv []string, stdin []byte) error {
			f.calls = append(f.calls, argv)
			f.stdins = append(f.stdins, stdin)
			if f.failOn != "" && slices.Contains(argv, f.failOn) {
				return errors.New("boom")
			}
			return nil
		},
		Exists: func(path string) bool { return f.existing[path] },
		Verify: func(path string, _ []byte, _ fs.FileMode) error {
			f.verified = append(f.verified, path)
			return nil
		},
		Log: &bytes.Buffer{},
	}
}

func TestApplyInstall(t *testing.T) {
	f := &fakeSystem{existing: systemDirs()}
	p := Build(testInputs(t))
	if err := f.applier().Apply(p); err != nil {
		t.Fatal(err)
	}

	// First call authenticates sudo.
	if !slices.Equal(f.calls[0], []string{sudo, "-v"}) {
		t.Errorf("first call = %v, want sudo -v", f.calls[0])
	}
	// Every call goes through /usr/bin/sudo.
	for _, c := range f.calls {
		if c[0] != sudo {
			t.Errorf("call without sudo: %v", c)
		}
	}
	// /etc/rig is missing in the fake system, so it is created first.
	mkdir := []string{sudo, "/usr/bin/install", "-d", "-o", "root", "-g", "root", "-m", "0755", "--", "/etc/rig"}
	if !slices.Equal(f.calls[1], mkdir) {
		t.Errorf("second call = %v, want %v", f.calls[1], mkdir)
	}
	// Files are written from stdin, with content passed in memory.
	write := []string{sudo, "/usr/bin/install", "-o", "root", "-g", "root", "-m", "644", "/dev/stdin", natConfPath}
	i := slices.IndexFunc(f.calls, func(c []string) bool { return slices.Equal(c, write) })
	if i < 0 {
		t.Fatalf("no write call for %s", natConfPath)
	}
	if !bytes.Equal(f.stdins[i], natRules()) {
		t.Error("nat.nft was not streamed with the right content")
	}
	// Each written file is verified afterwards.
	for _, want := range []string{natConfPath, natUnitPath, TrustAnchorPath} {
		if !slices.Contains(f.verified, want) {
			t.Errorf("%s was not verified", want)
		}
	}
}

func TestApplyStopsOnFailure(t *testing.T) {
	f := &fakeSystem{existing: systemDirs("/etc/rig"), failOn: updateCATrust}
	err := f.applier().Apply(Build(testInputs(t)))
	if err == nil || !strings.Contains(err.Error(), "rebuild the system trust store") {
		t.Fatalf("Apply = %v, want failure at update-ca-trust", err)
	}
	// Nothing after the failing step ran.
	for _, c := range f.calls {
		if slices.Contains(c, "enable") {
			t.Error("enable ran after an earlier step failed")
		}
	}
}

func TestApplyOptionalFailureContinues(t *testing.T) {
	f := &fakeSystem{existing: systemDirs("/etc/rig"), failOn: "disable"}
	if err := f.applier().Apply(BuildUninstall()); err != nil {
		t.Fatalf("Apply = %v; optional failure should not stop the plan", err)
	}
	last := f.calls[len(f.calls)-1]
	if !slices.Contains(last, "daemon-reload") {
		t.Errorf("last call = %v, want daemon-reload (plan should finish)", last)
	}
}

func TestApplyRejectsInvalidPlan(t *testing.T) {
	f := &fakeSystem{}
	bad := &Plan{Title: "x", Actions: []Action{file("/etc/passwd", 0o644)}}
	if err := f.applier().Apply(bad); err == nil {
		t.Fatal("Apply of an invalid plan succeeded")
	}
	if len(f.calls) != 0 {
		t.Errorf("commands ran for an invalid plan: %v", f.calls)
	}
}

func TestUninstallPlan(t *testing.T) {
	p := BuildUninstall()
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	removed := map[string]bool{}
	for _, a := range p.Actions {
		if a.Kind == RemoveFile {
			removed[a.Path] = true
		}
	}
	// Uninstall must remove every file install writes.
	for _, a := range Build(testInputs(t)).Actions {
		if a.Kind == WriteFile && !removed[a.Path] {
			t.Errorf("uninstall does not remove %s", a.Path)
		}
	}
}

func TestRemoveDirSkippedWhenMissing(t *testing.T) {
	f := &fakeSystem{existing: systemDirs()}
	if err := f.applier().Apply(BuildUninstall()); err != nil {
		t.Fatal(err)
	}
	for _, c := range f.calls {
		if slices.Contains(c, "/usr/bin/rmdir") {
			t.Error("rmdir ran for a folder that does not exist")
		}
	}
}

func TestApplyRefusesToCreateSystemFolders(t *testing.T) {
	f := &fakeSystem{existing: systemDirs()}
	delete(f.existing, "/etc/systemd/system") // pretend it is missing
	if err := f.applier().Apply(Build(testInputs(t))); err == nil {
		t.Fatal("Apply created a folder outside the allowlist")
	}
	for _, c := range f.calls {
		if slices.Contains(c, "-d") && !slices.Contains(c, "/etc/rig") {
			t.Errorf("tried to create a folder other than /etc/rig: %v", c)
		}
	}
}

func TestApplyPreflightMissingProgram(t *testing.T) {
	f := &fakeSystem{existing: systemDirs()}
	delete(f.existing, "/usr/bin/nft")
	err := f.applier().Apply(BuildUninstall())
	if err == nil || !strings.Contains(err.Error(), "/usr/bin/nft not found") {
		t.Fatalf("Apply = %v, want missing-program error", err)
	}
	if len(f.calls) != 0 {
		t.Errorf("commands ran before the pre-flight check passed: %v", f.calls)
	}
}

func TestRollbackNeverStops(t *testing.T) {
	f := &fakeSystem{existing: systemDirs("/etc/rig"), failOn: "/usr/bin/rm"}
	if err := f.applier().Apply(BuildRollback()); err != nil {
		t.Fatalf("rollback stopped: %v", err)
	}
	last := f.calls[len(f.calls)-1]
	if !slices.Contains(last, "daemon-reload") {
		t.Errorf("rollback did not reach the end; last call %v", last)
	}
}
