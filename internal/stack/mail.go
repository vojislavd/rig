package stack

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/vojislavd/rig/internal/fsutil"
	"github.com/vojislavd/rig/internal/mailpit"
)

// mailDir is where Mailpit keeps caught mail.
func (st *Stack) mailDir() string {
	return filepath.Join(filepath.Dir(st.L.Certs), "mailpit")
}

// StartMail starts Mailpit now. It is on demand: it never starts by
// itself, and logging out stops it. While it is stopped, sites that send
// mail get a connection error.
func (st *Stack) StartMail() error {
	if err := fsutil.EnsurePrivateDir(st.mailDir()); err != nil {
		return err
	}
	unit, err := mailpit.Unit(st.mailDir())
	if err != nil {
		return err
	}
	if err := os.MkdirAll(st.L.Units, 0o755); err != nil {
		return err
	}
	changed, err := writeIfChanged(file{path: filepath.Join(st.L.Units, mailpit.UnitName), content: unit, mode: 0o644})
	if err != nil {
		return err
	}
	if changed {
		if err := st.S.DaemonReload(); err != nil {
			return err
		}
	}
	return st.S.Start(mailpit.UnitName)
}

// StopMail stops Mailpit and removes its unit. Caught mail is kept.
func (st *Stack) StopMail() error {
	path := filepath.Join(st.L.Units, mailpit.UnitName)
	if !fileExists(path) {
		return nil
	}
	if err := st.S.Stop(mailpit.UnitName); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return st.S.DaemonReload()
}

// MailState returns systemd's state word for Mailpit ("active",
// "inactive", ...), or "stopped" if it was never started.
func (st *Stack) MailState() string {
	if !fileExists(filepath.Join(st.L.Units, mailpit.UnitName)) {
		return "stopped"
	}
	return st.S.IsActive(mailpit.UnitName)
}
