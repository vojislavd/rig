package fsutil

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.lock")

	unlock, err := Lock(path, time.Second)
	if err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("lock file mode = %o, want 600", mode)
	}

	// While held, a second Lock must time out. flock locks belong to an
	// open file, so this works even inside one test process.
	if _, err := Lock(path, 100*time.Millisecond); !errors.Is(err, ErrLocked) {
		t.Errorf("second Lock = %v, want ErrLocked", err)
	}

	if err := unlock(); err != nil {
		t.Fatal(err)
	}

	// After release, it can be taken again.
	unlock2, err := Lock(path, time.Second)
	if err != nil {
		t.Fatalf("Lock after unlock: %v", err)
	}
	unlock2()
}

func TestLockRefusesSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	link := filepath.Join(dir, "test.lock")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Lock(link, time.Second); err == nil {
		t.Error("Lock through a symlink succeeded, want error")
	}
}
