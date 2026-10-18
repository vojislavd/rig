package worker

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestUnit(t *testing.T) {
	unit, err := Unit("shop", "/home/u/code/shop", "queue", "/usr/bin/php", 0)
	if err != nil {
		t.Fatal(err)
	}
	s := string(unit)
	for _, want := range []string{
		"WorkingDirectory=/home/u/code/shop\n",
		"ExecStart=/usr/bin/php artisan queue:work --sleep=3 --tries=3 --max-time=3600\n",
		"Restart=always\n",
		"ProtectHome=tmpfs\n",
		"BindPaths=/home/u/code/shop\n",
		"NoNewPrivileges=yes\n",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("unit missing %q", want)
		}
	}
	// Only this project is visible: never the whole code folder or home.
	// "[Install]" would let it start at login; workers are on demand only.
	for _, banned := range []string{"BindPaths=/home/u/code\n", "BindPaths=/home/u\n", "ProtectHome=no", "MemoryDenyWriteExecute", "[Install]", "WantedBy"} {
		if strings.Contains(s, banned) {
			t.Errorf("unit contains %q", banned)
		}
	}
	if got := UnitName("shop", "queue"); got != "rig-shop-queue.service" {
		t.Errorf("UnitName = %q", got)
	}
}

func TestUnitSchedule(t *testing.T) {
	unit, err := Unit("shop", "/home/u/code/shop", "schedule", "/usr/bin/php", 0)
	if err != nil || !strings.Contains(string(unit), "ExecStart=/usr/bin/php artisan schedule:work\n") {
		t.Errorf("schedule unit wrong: %v\n%s", err, unit)
	}
}

func TestUnitRejects(t *testing.T) {
	cases := []struct{ site, dir, kind, php string }{
		{"Shop", "/home/u/code/shop", "queue", "/usr/bin/php"},
		{"shop", "/home/u/code/my shop", "queue", "/usr/bin/php"},
		{"shop", "/home/u/code/shop", "tinker", "/usr/bin/php"},
		{"shop", "/home/u/code/shop", "queue; rm", "/usr/bin/php"},
		{"shop", "/home/u/code/shop", "queue", "php"},
		{"shop", "/home/u/code/shop\nExecStart=/bin/sh", "queue", "/usr/bin/php"},
	}
	for _, c := range cases {
		if _, err := Unit(c.site, c.dir, c.kind, c.php, 0); err == nil {
			t.Errorf("Unit accepted %+v", c)
		}
	}
}

// TestUnitSystemdAccepts runs "systemd-analyze verify" on a generated
// unit, if available. It only reads the file.
func TestUnitSystemdAccepts(t *testing.T) {
	if _, err := exec.LookPath("systemd-analyze"); err != nil {
		t.Skip("systemd-analyze not available")
	}
	if _, err := os.Stat("/usr/bin/php"); err != nil {
		t.Skip("php not installed")
	}
	dir := t.TempDir()
	unit, err := Unit("shop", dir, "queue", "/usr/bin/php", 0)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "rig-shop-queue.service")
	os.WriteFile(path, unit, 0o644)
	if out, err := exec.Command("systemd-analyze", "verify", "--user", path).CombinedOutput(); err != nil {
		t.Fatalf("systemd-analyze verify: %v\n%s", err, out)
	}
}

func TestUnitReverb(t *testing.T) {
	unit, err := Unit("shop", "/home/u/code/shop", "reverb", "/usr/bin/php", 6001)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(unit), "ExecStart=/usr/bin/php artisan reverb:start --host=127.0.0.1 --port=6001\n") {
		t.Errorf("reverb unit:\n%s", unit)
	}
	if strings.Contains(string(unit), "0.0.0.0") {
		t.Error("reverb may listen on all interfaces")
	}
	for _, port := range []int{0, 80, 8080, 7000} {
		if _, err := Unit("shop", "/home/u/code/shop", "reverb", "/usr/bin/php", port); err == nil {
			t.Errorf("reverb accepted port %d", port)
		}
	}
	if _, err := Unit("shop", "/home/u/code/shop", "queue", "/usr/bin/php", 6001); err == nil {
		t.Error("queue accepted a port")
	}
}
