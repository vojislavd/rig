package fsutil

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnsurePrivateDir(t *testing.T) {
	root := t.TempDir()

	// Creates missing folders, including parents, as 0700.
	path := filepath.Join(root, "a", "b")
	if err := EnsurePrivateDir(path); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{filepath.Join(root, "a"), path} {
		info, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if mode := info.Mode().Perm(); mode != 0o700 {
			t.Errorf("%s mode = %o, want 700", p, mode)
		}
	}

	// Calling it again on a private folder is fine.
	if err := EnsurePrivateDir(path); err != nil {
		t.Errorf("second call: %v", err)
	}

	// An existing folder that others can read is refused, not fixed.
	open := filepath.Join(root, "open")
	if err := os.Mkdir(open, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(open, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := EnsurePrivateDir(open); err == nil {
		t.Error("EnsurePrivateDir on a 0755 folder succeeded, want error")
	}
}

func TestCheckPrivate(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "dir")
	file := filepath.Join(root, "file")
	loose := filepath.Join(root, "loose")
	link := filepath.Join(root, "link")

	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(loose, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(loose, 0o640); err != nil { // group can read
		t.Fatal(err)
	}
	if err := os.Symlink(file, link); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		path    string
		wantDir bool
		wantOK  bool
	}{
		{"private folder", dir, true, true},
		{"private file", file, false, true},
		{"group-readable file", loose, false, false},
		{"symlink to a private file", link, false, false},
		{"file where a folder is expected", file, true, false},
		{"folder where a file is expected", dir, false, false},
		{"missing", filepath.Join(root, "missing"), false, false},
	}
	for _, tt := range tests {
		err := CheckPrivate(tt.path, tt.wantDir)
		if tt.wantOK && err != nil {
			t.Errorf("%s: %v", tt.name, err)
		}
		if !tt.wantOK && err == nil {
			t.Errorf("%s: no error, want one", tt.name)
		}
	}
}
