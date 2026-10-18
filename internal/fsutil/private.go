package fsutil

import (
	"fmt"
	"os"
	"syscall"
)

// EnsurePrivateDir creates path (and missing parents) with mode 0700, or,
// if it already exists, checks that it is private (see CheckPrivate).
//
// An existing folder with looser permissions is reported, not silently
// fixed: it may mean someone else could already have read its contents.
func EnsurePrivateDir(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	return CheckPrivate(path, true)
}

// CheckPrivate verifies that path is safe for secrets:
//   - not a symlink, which could point somewhere unexpected
//   - a folder if wantDir, otherwise a regular file
//   - owned by the current user
//   - no permissions for group or others (mode & 0077 == 0)
func CheckPrivate(path string, wantDir bool) error {
	// Lstat looks at the path itself and does not follow a symlink.
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}

	mode := info.Mode()
	switch {
	case mode&os.ModeSymlink != 0:
		return fmt.Errorf("%s is a symlink, refusing to use it", path)
	case wantDir && !mode.IsDir():
		return fmt.Errorf("%s is not a folder", path)
	case !wantDir && !mode.IsRegular():
		return fmt.Errorf("%s is not a regular file", path)
	}

	// Sys() exposes the raw Linux file info, which holds the owner's uid.
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("%s: can't read file owner", path)
	}
	if int(st.Uid) != os.Getuid() {
		return fmt.Errorf("%s is owned by uid %d, not by you", path, st.Uid)
	}

	if perm := mode.Perm(); perm&0o077 != 0 {
		return fmt.Errorf("%s has mode %04o; group/others must have no access (fix: chmod go-rwx %s)", path, perm, path)
	}
	return nil
}
