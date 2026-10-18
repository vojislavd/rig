package stack

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/vojislavd/rig/internal/site"
)

// DBDropper is the part of db.Client that rig rm uses.
type DBDropper interface {
	Exists(siteName string) (bool, error)
	Dump(siteName string) (backup string, err error)
	Drop(siteName string) error
}

// RmPreview is what rig rm will do, shown before anything happens.
type RmPreview struct {
	Name     string   `json:"name"`
	Dir      string   `json:"dir"`
	DBExists bool     `json:"db_exists"`
	Purge    bool     `json:"purge"`
	Warnings []string `json:"warnings,omitempty"` // e.g. unpushed git commits
}

// PlanRemove checks the site and describes what removing it will do.
func (st *Stack) PlanRemove(name string, dbc DBDropper, purge bool) (RmPreview, error) {
	if err := site.ValidateName(name); err != nil {
		return RmPreview{}, err
	}
	r, err := site.Load(st.L.Registry())
	if err != nil {
		return RmPreview{}, err
	}
	s, ok := r.Get(name)
	if !ok {
		return RmPreview{}, fmt.Errorf("%w: %q", site.ErrNotFound, name)
	}
	exists, err := dbc.Exists(name)
	if err != nil {
		return RmPreview{}, err
	}
	p := RmPreview{Name: name, Dir: s.Dir, DBExists: exists, Purge: purge}
	if purge {
		p.Warnings = gitWarnings(s.Dir)
	}
	return p, nil
}

// Remove carries out a confirmed PlanRemove, in this order:
//  1. back up the database; if that fails, nothing else happens
//  2. unlink (stop serving), so the app never runs without its database
//  3. drop the database and its user
//  4. with purge, delete the project folder
//
// Returns the backup path, if any.
func (st *Stack) Remove(p RmPreview, dbc DBDropper) (backup string, err error) {
	if p.DBExists {
		if backup, err = dbc.Dump(p.Name); err != nil {
			return "", fmt.Errorf("backup failed, nothing changed: %w", err)
		}
	}
	if err := st.Unlink(p.Name); err != nil && !errors.Is(err, site.ErrNotFound) {
		return backup, fmt.Errorf("unlink failed (database kept; backup %s): %w", backup, err)
	}
	if err := dbc.Drop(p.Name); err != nil {
		return backup, fmt.Errorf("site unlinked, but dropping the database failed: %w", err)
	}
	if !p.Purge {
		return backup, nil
	}
	// 3. Purge. Check the folder again right before deleting: it must
	//    still be a real folder (not a symlink) directly inside the code
	//    folder, named after the site.
	real, err := site.SiteDir(st.L.Code, p.Dir)
	if err != nil {
		return backup, fmt.Errorf("refusing to delete %s: %w", p.Dir, err)
	}
	if real != p.Dir || filepath.Base(real) != p.Name {
		return backup, fmt.Errorf("refusing to delete %s: it no longer matches the site folder", p.Dir)
	}
	if info, err := os.Lstat(p.Dir); err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return backup, fmt.Errorf("refusing to delete %s: not a plain folder", p.Dir)
	}
	if err := os.RemoveAll(p.Dir); err != nil {
		return backup, err
	}
	return backup, nil
}

// gitWarnings reports uncommitted changes and unpushed commits.
//
// Security: a cloned repository's own .git/config could set
// core.fsmonitor to a command that "git status" would run. rig turns it
// off on the command line, which overrides the repository's setting.
func gitWarnings(dir string) []string {
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		return []string{"not a git repository: the code exists only here"}
	}
	git := func(args ...string) (string, error) {
		base := []string{"-C", dir, "-c", "core.fsmonitor=false", "-c", "core.untrackedCache=false"}
		cmd := exec.Command("/usr/bin/git", append(base, args...)...)
		var out bytes.Buffer
		cmd.Stdout = &out
		err := cmd.Run()
		return strings.TrimSpace(out.String()), err
	}
	var w []string
	if out, err := git("status", "--porcelain"); err != nil {
		w = append(w, "could not run git status")
	} else if out != "" {
		w = append(w, fmt.Sprintf("%d uncommitted change(s)", len(strings.Split(out, "\n"))))
	}
	if out, err := git("log", "--oneline", "@{upstream}..HEAD"); err != nil {
		w = append(w, "no upstream branch: commits may exist only here")
	} else if out != "" {
		w = append(w, fmt.Sprintf("%d commit(s) not pushed", len(strings.Split(out, "\n"))))
	}
	return w
}
