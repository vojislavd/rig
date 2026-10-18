package db

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAdminFileRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mariadb.cnf")
	pw, _ := NewPassword()
	if err := SaveAdmin(path, pw); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode %o, want 600", info.Mode().Perm())
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "user=rigadmin\n") {
		t.Error("no user line")
	}
	got, err := LoadAdmin(path)
	if err != nil || got != pw {
		t.Errorf("LoadAdmin = %q, %v", got, err)
	}
}

func TestLoadAdminRejects(t *testing.T) {
	dir := t.TempDir()
	if _, err := LoadAdmin(filepath.Join(dir, "missing")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("missing file: %v, want ErrNotExist", err)
	}
	loose := filepath.Join(dir, "loose.cnf")
	pw, _ := NewPassword()
	SaveAdmin(loose, pw)
	os.Chmod(loose, 0o644)
	if _, err := LoadAdmin(loose); err == nil {
		t.Error("accepted a world-readable login file")
	}
	bad := filepath.Join(dir, "bad.cnf")
	os.WriteFile(bad, []byte("[client]\npassword=x'y\n"), 0o600)
	if _, err := LoadAdmin(bad); err == nil {
		t.Error("accepted an invalid password")
	}
}
