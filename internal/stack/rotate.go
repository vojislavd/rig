package stack

import (
	"errors"
	"io/fs"
	"os"

	"github.com/vojislavd/rig/internal/ca"
)

// "rig ca rotate" replaces the CA in these steps:
//  1. PendingCA: a new CA, kept in <ca>.new until it is in use
//  2. the caller trusts it with sudo (install.BuildTrust)
//  3. SwapCA: <ca>.new becomes <ca>; the old CA is deleted
//  4. Repair: every site certificate is reissued and nginx reloads
//
// If rotation stops part way, running it again picks up the same new CA
// from <ca>.new. The old CA is never needed, so rotation also works when
// the old one fails its checks (for example a key with a loose mode).

func (st *Stack) pendingDir() string { return st.L.CA + ".new" }

// PendingCA returns the CA waiting in <ca>.new, or a new one in memory
// (isNew) to be saved with SavePendingCA once the user agrees.
func (st *Stack) PendingCA() (c *ca.CA, isNew bool, err error) {
	c, err = ca.Load(st.pendingDir())
	if err == nil {
		return c, false, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return nil, false, err
	}
	c, err = ca.New(st.Now())
	return c, true, err
}

// SavePendingCA stores a new CA in <ca>.new (folder 0700, key 0600).
func (st *Stack) SavePendingCA(c *ca.CA) error {
	return c.Save(st.pendingDir())
}

// SwapCA makes the pending CA the current one and deletes the old CA.
// Each rename is atomic. The old key is deleted right away: once the
// system trusts the new CA, nothing may sign with the old one again.
func (st *Stack) SwapCA() error {
	if _, err := ca.Load(st.pendingDir()); err != nil {
		return err
	}
	unlock, err := st.lockRegistry()
	if err != nil {
		return err
	}
	defer unlock()

	old := st.L.CA + ".old"
	if err := os.RemoveAll(old); err != nil {
		return err
	}
	if err := os.Rename(st.L.CA, old); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := os.Rename(st.pendingDir(), st.L.CA); err != nil {
		return err
	}
	return os.RemoveAll(old)
}
