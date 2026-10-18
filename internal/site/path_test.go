package site

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSiteDir(t *testing.T) {
	// t.TempDir creates a fresh folder that is deleted after the test,
	// so we can build a fake ~/code without touching the real one.
	// It is resolved too, in case the temp dir itself sits behind a symlink.
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	code := filepath.Join(root, "code")
	outside := filepath.Join(root, "outside")

	mkdir(t, code)
	mkdir(t, outside)
	mkdir(t, filepath.Join(code, "foo"))
	mkdir(t, filepath.Join(code, "foo", "sub"))
	writeFile(t, filepath.Join(code, "file"))
	symlink(t, outside, filepath.Join(code, "escape"))                   // points out of code
	symlink(t, filepath.Join(code, "foo"), filepath.Join(code, "alias")) // points to code/foo

	// The code dir itself reached through a symlink (e.g. ~/code -> /data/code).
	codeLink := filepath.Join(root, "codelink")
	symlink(t, code, codeLink)

	t.Run("accepted", func(t *testing.T) {
		tests := []struct {
			name, codeDir, dir, want string
		}{
			{"direct child", code, filepath.Join(code, "foo"), filepath.Join(code, "foo")},
			{"symlink to a child resolves to the real folder", code, filepath.Join(code, "alias"), filepath.Join(code, "foo")},
			{"code dir is a symlink", codeLink, filepath.Join(codeLink, "foo"), filepath.Join(code, "foo")},
		}
		for _, tt := range tests {
			got, err := SiteDir(tt.codeDir, tt.dir)
			if err != nil {
				t.Errorf("%s: SiteDir(%q) error: %v", tt.name, tt.dir, err)
				continue
			}
			if got != tt.want {
				t.Errorf("%s: SiteDir(%q) = %q, want %q", tt.name, tt.dir, got, tt.want)
			}
		}
	})

	t.Run("rejected", func(t *testing.T) {
		tests := []struct {
			name, codeDir, dir string
		}{
			{"nested too deep", code, filepath.Join(code, "foo", "sub")},
			{"the code dir itself", code, code},
			{"outside the code dir", code, outside},
			{"dot-dot escape", code, filepath.Join(code, "..", "outside")},
			{"symlink escape", code, filepath.Join(code, "escape")},
			{"does not exist", code, filepath.Join(code, "missing")},
			{"a file, not a folder", code, filepath.Join(code, "file")},
			{"relative dir", code, "foo"},
			{"relative code dir", "code", filepath.Join(code, "foo")},
		}
		for _, tt := range tests {
			if got, err := SiteDir(tt.codeDir, tt.dir); err == nil {
				t.Errorf("%s: SiteDir(%q) = %q, want error", tt.name, tt.dir, got)
			}
		}
	})
}

// Small helpers that stop the test on setup errors.

func mkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
}

func writeFile(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
}

func symlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}
