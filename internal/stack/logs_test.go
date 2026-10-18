package stack

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/vojislavd/rig/internal/site"
)

func TestLogSource(t *testing.T) {
	st, dir := linkedStack(t)
	logs := filepath.Join(dir, "storage", "logs")
	os.MkdirAll(logs, 0o700)

	for target, want := range map[string]LogSource{
		"nginx":        {File: filepath.Join(st.L.Logs, "nginx-error.log")},
		"nginx:access": {File: filepath.Join(st.L.Logs, "nginx-access.log")},
		"php":          {File: filepath.Join(st.L.Logs, "php85-fpm.log")},
		"mail":         {Unit: "rig-mailpit.service"},
		"s3":           {Unit: "rig-garage.service"},
		"foo:queue":    {Unit: "rig-foo-queue.service"},
		"foo":          {File: filepath.Join(logs, "laravel.log")}, // none yet: tail -F waits
	} {
		got, err := st.LogSource(target)
		if err != nil || got != want {
			t.Errorf("%s = %+v, %v; want %+v", target, got, err, want)
		}
	}

	// Daily channel: the newest laravel-*.log wins; other logs don't count.
	old, newer := filepath.Join(logs, "laravel-2026-10-16.log"), filepath.Join(logs, "laravel-2026-10-17.log")
	for i, p := range []string{old, newer, filepath.Join(logs, "browser.log")} {
		os.WriteFile(p, nil, 0o600)
		ts := time.Now().Add(time.Duration(i-3) * time.Minute)
		os.Chtimes(p, ts, ts)
	}
	if got, _ := st.LogSource("foo"); got.File != newer {
		t.Errorf("foo = %s, want %s", got.File, newer)
	}
	// A symlink named like a log is skipped, even when it is the newest.
	os.Symlink("/etc/passwd", filepath.Join(logs, "laravel-9999.log"))
	if got, _ := st.LogSource("foo"); got.File != newer {
		t.Errorf("symlink chosen: %s", got.File)
	}

	for _, bad := range []string{"", "../x", "Foo", "foo:evil", "nope", "nope:queue", "nginx:error"} {
		if _, err := st.LogSource(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	if _, err := st.LogSource("nope"); !errors.Is(err, site.ErrNotFound) {
		t.Errorf("unknown site: %v", err)
	}
}

func TestLogSourceStorageOutsideProject(t *testing.T) {
	st, dir := linkedStack(t)
	if _, err := st.LogSource("foo"); err == nil {
		t.Error("accepted a project without storage/logs")
	}
	outside := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "storage"), 0o700)
	os.Symlink(outside, filepath.Join(dir, "storage", "logs"))
	if _, err := st.LogSource("foo"); err == nil {
		t.Error("followed storage/logs out of the project")
	}
}
