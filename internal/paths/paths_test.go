package paths

import "testing"

func TestDataDir(t *testing.T) {
	tests := []struct {
		name, xdg, home, want string
	}{
		{"XDG set", "/data", "/home/u", "/data/rig"},
		{"XDG unset", "", "/home/u", "/home/u/.local/share/rig"},
		{"XDG relative is ignored", "rel/dir", "/home/u", "/home/u/.local/share/rig"},
		{"XDG cleaned", "/data/../data/", "/home/u", "/data/rig"},
	}
	for _, tt := range tests {
		// t.Setenv changes an environment variable for this test only.
		t.Setenv("XDG_DATA_HOME", tt.xdg)
		t.Setenv("HOME", tt.home)
		got, err := DataDir()
		if err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}
		if got != tt.want {
			t.Errorf("%s: DataDir() = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestSubDirs(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "/data")
	if got, _ := CADir(); got != "/data/rig/ca" {
		t.Errorf("CADir() = %q", got)
	}
	if got, _ := CertsDir(); got != "/data/rig/certs" {
		t.Errorf("CertsDir() = %q", got)
	}
}

func TestRelativeHomeRejected(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("HOME", "relative")
	if _, err := DataDir(); err == nil {
		t.Error("want error for a relative home folder")
	}
}

func TestConfigStateRuntime(t *testing.T) {
	t.Setenv("HOME", "/home/u")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")
	if got, _ := ConfigDir(); got != "/home/u/.config/rig" {
		t.Errorf("ConfigDir() = %q", got)
	}
	if got, _ := StateDir(); got != "/home/u/.local/state/rig" {
		t.Errorf("StateDir() = %q", got)
	}
	if got, _ := RuntimeDir(); got != "/run/user/1000/rig" {
		t.Errorf("RuntimeDir() = %q", got)
	}
	for _, bad := range []string{"", "relative"} {
		t.Setenv("XDG_RUNTIME_DIR", bad)
		if _, err := RuntimeDir(); err == nil {
			t.Errorf("RuntimeDir with XDG_RUNTIME_DIR=%q: want error", bad)
		}
	}
}
