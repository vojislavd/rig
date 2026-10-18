// Package fsutil holds small file helpers shared across rig.
package fsutil

import (
	"os"
	"path/filepath"
)

// WriteFileAtomic writes data to path so that readers only ever see the
// old content or the complete new content, never a half-written file.
//
// It writes to a temp file in the same folder, flushes it to disk, sets
// perm, then renames it over path. A rename within one folder is atomic
// on Linux. The temp file is created with mode 0600 from the start, so
// the data is never readable by others, even briefly.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) (err error) {
	dir := filepath.Dir(path)

	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	// If anything below fails, remove the temp file. "err" is a named
	// return value, so this deferred function sees the final error.
	defer func() {
		if err != nil {
			tmp.Close()
			os.Remove(tmp.Name())
		}
	}()

	if _, err = tmp.Write(data); err != nil {
		return err
	}
	// Sync forces the data onto the disk before the rename, so a power
	// cut can't leave an empty file under the real name.
	if err = tmp.Sync(); err != nil {
		return err
	}
	// Chmod explicitly: the mode given to create is reduced by the umask.
	if err = tmp.Chmod(perm); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
