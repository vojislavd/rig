package mailpit

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestUnit(t *testing.T) {
	unit, err := Unit("/home/u/.local/share/rig/mailpit")
	if err != nil {
		t.Fatal(err)
	}
	s := string(unit)
	for _, want := range []string{
		"--smtp 127.0.0.1:1025 --listen 127.0.0.1:8025",
		"--database /home/u/.local/share/rig/mailpit/mailpit.db",
		"--allowed-hosts localhost ",
		"--disable-version-check",
		"--block-remote-css-and-fonts",
		"ProtectHome=tmpfs\n",
		"BindPaths=/home/u/.local/share/rig/mailpit\n",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("unit missing %q", want)
		}
	}
	// Mail must never leave the machine, Mailpit must never listen beyond
	// loopback, and it must not start by itself.
	for _, banned := range []string{"relay", "0.0.0.0", "[::]", "[Install]", "WantedBy"} {
		if strings.Contains(s, banned) {
			t.Errorf("unit contains %q", banned)
		}
	}
}

func TestUnitRejectsUnsafePath(t *testing.T) {
	if _, err := Unit("/home/u/my mail"); err == nil {
		t.Error("accepted a path with a space")
	}
}

func TestUnitSystemdAccepts(t *testing.T) {
	if _, err := exec.LookPath("systemd-analyze"); err != nil {
		t.Skip("systemd-analyze not available")
	}
	if _, err := os.Stat("/usr/bin/mailpit"); err != nil {
		t.Skip("mailpit not installed")
	}
	unit, err := Unit(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), UnitName)
	os.WriteFile(path, unit, 0o644)
	if out, err := exec.Command("systemd-analyze", "verify", "--user", path).CombinedOutput(); err != nil {
		t.Fatalf("systemd-analyze verify: %v\n%s", err, out)
	}
}
