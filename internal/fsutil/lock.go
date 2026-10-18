package fsutil

import (
	"errors"
	"os"
	"syscall"
	"time"
)

// ErrLocked means another process held the lock until the timeout ran out.
var ErrLocked = errors.New("locked by another rig process")

// pollInterval is how often Lock retries while the lock is busy.
const pollInterval = 50 * time.Millisecond

// Lock takes an exclusive lock on the file at path, creating it (mode
// 0600) if needed, and returns a function that releases it. If another
// process holds the lock, Lock retries until timeout, then returns
// ErrLocked.
//
// It uses flock(2): the kernel releases the lock automatically if the
// process dies, so a crash can't leave a stale lock behind.
func Lock(path string, timeout time.Duration) (unlock func() error, err error) {
	// O_NOFOLLOW: refuse to open the lock file through a symlink.
	// O_CLOEXEC: don't leak the open file into programs rig starts.
	fd, err := syscall.Open(path, syscall.O_RDWR|syscall.O_CREAT|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0o600)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	f := os.NewFile(uintptr(fd), path)

	deadline := time.Now().Add(timeout)
	for {
		// LOCK_NB: return at once if busy, instead of waiting forever.
		err = syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) || time.Now().After(deadline) {
			f.Close()
			if errors.Is(err, syscall.EWOULDBLOCK) {
				return nil, ErrLocked
			}
			return nil, &os.PathError{Op: "flock", Path: path, Err: err}
		}
		time.Sleep(pollInterval)
	}

	// Closing the file releases the lock.
	return f.Close, nil
}
