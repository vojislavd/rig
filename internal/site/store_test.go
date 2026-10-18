package site

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sites.json")

	r := NewRegistry()
	for _, n := range []string{"foo", "bar"} {
		if err := r.Add(Site{Name: n, Dir: "/home/u/code/" + n}); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.Save(path); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("mode = %o, want 600", mode)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	got := loaded.List()
	if len(got) != 2 || got[0].Name != "bar" || got[1].Name != "foo" {
		t.Errorf("loaded sites = %+v, want bar and foo", got)
	}
}

func TestLoadMissingFile(t *testing.T) {
	r, err := Load(filepath.Join(t.TempDir(), "sites.json"))
	if err != nil {
		t.Fatal(err)
	}
	if n := len(r.List()); n != 0 {
		t.Errorf("got %d sites, want 0", n)
	}
}

func TestLoadRejects(t *testing.T) {
	tests := []struct {
		name, content string
	}{
		{"not JSON", `nope`},
		{"empty file", ``},
		{"wrong version", `{"version": 2, "sites": []}`},
		{"missing version", `{"sites": []}`},
		{"unknown field", `{"version": 1, "sites": [], "extra": true}`},
		{"unknown site field", `{"version": 1, "sites": [{"name": "foo", "dir": "/c/foo", "php": "8.5"}]}`},
		{"trailing data", `{"version": 1, "sites": []} {}`},
		{"bad name", `{"version": 1, "sites": [{"name": "../etc", "dir": "/etc"}]}`},
		{"folder mismatch", `{"version": 1, "sites": [{"name": "foo", "dir": "/c/bar"}]}`},
		{"duplicate", `{"version": 1, "sites": [{"name": "foo", "dir": "/c/foo"}, {"name": "foo", "dir": "/c/foo"}]}`},
		// Valid JSON padded with spaces, so only the size limit can reject it.
		{"too big", `{"version": 1, "sites": []}` + strings.Repeat(" ", maxFileSize)},
	}
	for _, tt := range tests {
		path := filepath.Join(t.TempDir(), "sites.json")
		if err := os.WriteFile(path, []byte(tt.content), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(path); err == nil {
			t.Errorf("%s: Load = nil error, want error", tt.name)
		}
	}
}
