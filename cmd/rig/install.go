package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vojislavd/rig/internal/ca"
	"github.com/vojislavd/rig/internal/db"
	"github.com/vojislavd/rig/internal/fsutil"
	"github.com/vojislavd/rig/internal/install"
	"github.com/vojislavd/rig/internal/paths"
)

// stdin and newApplier are variables so tests can replace them: tests
// type answers without a terminal and never run real sudo commands.
var (
	stdin      io.Reader = os.Stdin
	newApplier           = install.NewApplier
)

// runInstall handles "rig install [--dry-run] [--show]".
func runInstall(args []string, stdout, stderr io.Writer) int {
	// A FlagSet parses the options of one subcommand. ContinueOnError
	// makes Parse return an error instead of exiting the program.
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dryRun := fs.Bool("dry-run", false, "show the changes without applying them")
	show := fs.Bool("show", false, "also print the full content of every file")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "rig install: unexpected argument %q\n", fs.Arg(0))
		return 2
	}

	dir, err := paths.CADir()
	if err != nil {
		fmt.Fprintf(stderr, "rig install: %v\n", err)
		return 1
	}
	c, isNew, err := loadOrNewCA(dir)
	if err != nil {
		fmt.Fprintf(stderr, "rig install: %v\n", err)
		return 1
	}
	if isNew {
		fmt.Fprintf(stdout, "No CA yet: this new one will be saved in %s if you continue.\n\n", dir)
	}

	cfg, err := paths.ConfigDir()
	if err != nil {
		fmt.Fprintf(stderr, "rig install: %v\n", err)
		return 1
	}
	adminFile := filepath.Join(cfg, "mariadb.cnf")
	adminPW, pwIsNew, err := loadOrNewAdminPassword(adminFile)
	if err != nil {
		fmt.Fprintf(stderr, "rig install: %v\n", err)
		return 1
	}
	setupSQL, err := db.AdminSetupSQL(adminPW)
	if err != nil {
		fmt.Fprintf(stderr, "rig install: %v\n", err)
		return 1
	}

	plan := install.Build(install.Inputs{
		CACert:       c.Cert.Raw,
		DBSetup:      setupSQL,
		DBSetupShown: db.Redact(setupSQL, adminPW),
	})
	if err := plan.Validate(); err != nil {
		fmt.Fprintf(stderr, "rig install: invalid plan: %v\n", err)
		return 1
	}
	plan.Render(stdout, *show)
	if *dryRun {
		return 0
	}

	if !confirm(stdout) {
		fmt.Fprintln(stderr, "rig install: aborted, nothing changed")
		return 1
	}
	// Save the CA and the MariaDB login (as the user, no sudo) only now
	// that the user agreed.
	if isNew {
		if err := c.Save(dir); err != nil {
			fmt.Fprintf(stderr, "rig install: save CA: %v\n", err)
			return 1
		}
	}
	if pwIsNew {
		if err := fsutil.EnsurePrivateDir(cfg); err != nil {
			fmt.Fprintf(stderr, "rig install: %v\n", err)
			return 1
		}
		if err := db.SaveAdmin(adminFile, adminPW); err != nil {
			fmt.Fprintf(stderr, "rig install: save MariaDB login: %v\n", err)
			return 1
		}
	}

	applier := newApplier(stderr)
	if err := applier.Apply(plan); err != nil {
		fmt.Fprintf(stderr, "\nrig install failed: %v\nRolling back...\n", err)
		// Rollback steps are all optional, so this only reports.
		applier.Apply(install.BuildRollback())
		fmt.Fprintln(stderr, "Rolled back. Nothing from rig install should remain; run rig uninstall --dry-run to see what it would remove.")
		return 1
	}
	fmt.Fprintln(stdout, "\nrig install: done. Restart your browsers so they load the new trust store.")
	return 0
}

// runUninstall handles "rig uninstall [--dry-run]".
func runUninstall(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("uninstall", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dryRun := fs.Bool("dry-run", false, "show the changes without applying them")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "rig uninstall: unexpected argument %q\n", fs.Arg(0))
		return 2
	}

	plan := install.BuildUninstall()
	plan.Render(stdout, false)
	if *dryRun {
		return 0
	}
	if !confirm(stdout) {
		fmt.Fprintln(stderr, "rig uninstall: aborted, nothing changed")
		return 1
	}
	if err := newApplier(stderr).Apply(plan); err != nil {
		fmt.Fprintf(stderr, "\nrig uninstall failed: %v\n", err)
		return 1
	}
	if dir, err := paths.CADir(); err == nil {
		fmt.Fprintf(stdout, "\nrig uninstall: done. The CA files in %s were kept (untrusted, harmless); delete them by hand if you like.\n", dir)
	}
	return 0
}

// loadOrNewCA loads the CA from dir, or creates a new one in memory if
// none exists yet (isNew). A CA that exists but fails the checks is an
// error: rig won't plan around a CA it doesn't trust.
func loadOrNewCA(dir string) (c *ca.CA, isNew bool, err error) {
	c, err = ca.Load(dir)
	if err == nil {
		return c, false, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return nil, false, fmt.Errorf("CA in %s: %w", dir, err)
	}
	c, err = ca.New(time.Now())
	if err != nil {
		return nil, false, err
	}
	return c, true, nil
}

// loadOrNewAdminPassword reads rig's MariaDB admin password, or makes a
// new one (isNew) if the login file doesn't exist yet. An existing file
// that fails the checks (not private, bad content) is an error.
func loadOrNewAdminPassword(path string) (pw string, isNew bool, err error) {
	pw, err = db.LoadAdmin(path)
	if err == nil {
		return pw, false, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return "", false, err
	}
	pw, err = db.NewPassword()
	return pw, true, err
}

// confirm asks "[y/N]". Only y or yes (any case) means yes; Enter alone,
// anything else, or no input at all means no, so a slip never applies
// system changes.
func confirm(stdout io.Writer) bool {
	return ask(stdout, "\nApply these changes?")
}

// ask prints question followed by [y/N] and reads the answer: only y or
// yes (any case) means yes.
func ask(stdout io.Writer, question string) bool {
	fmt.Fprint(stdout, question+" [y/N] ")
	line, _ := bufio.NewReader(stdin).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	}
	return false
}
