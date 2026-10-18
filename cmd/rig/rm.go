package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/vojislavd/rig/internal/db"
	"github.com/vojislavd/rig/internal/stack"
)

// runRm handles "rig rm <site> [--purge] [--yes] [--json]".
func runRm(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("rm", flag.ContinueOnError)
	fs.SetOutput(stderr)
	purge := fs.Bool("purge", false, "also delete the project folder (asks you to type the site name)")
	yes := fs.Bool("yes", false, "don't ask (not allowed with --purge)")
	asJSON := fs.Bool("json", false, "machine-readable output (needs --yes)")
	// Accept flags before and after the site name.
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() < 1 {
		fmt.Fprintln(stderr, "usage: rig rm <site> [--purge]")
		return 2
	}
	name := fs.Arg(0)
	if err := fs.Parse(fs.Args()[1:]); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "rig rm: unexpected argument %q\n", fs.Arg(0))
		return 2
	}
	// Deleting code always needs a typed confirmation; nothing skips it.
	if *purge && *yes {
		fmt.Fprintln(stderr, "rig rm: --purge can't be combined with --yes; you must type the site name")
		return 2
	}
	if *asJSON && !*yes {
		fmt.Fprintln(stderr, "rig rm: --json needs --yes")
		return 2
	}

	st, err := newStack()
	if err != nil {
		fmt.Fprintf(stderr, "rig rm: %v\n", err)
		return 1
	}
	dbc, err := newDBClient()
	if err != nil {
		fmt.Fprintf(stderr, "rig rm: %v\n", err)
		return 1
	}
	p, err := st.PlanRemove(name, dbc, *purge)
	if err != nil {
		fmt.Fprintf(stderr, "rig rm: %v\n", err)
		return 1
	}

	if !*asJSON {
		renderRmPreview(stdout, p)
	}
	switch {
	case *purge:
		if !confirmName(stdout, name) {
			fmt.Fprintln(stderr, "rig rm: name did not match, nothing changed")
			return 1
		}
	case !*yes:
		if !confirm(stdout) {
			fmt.Fprintln(stderr, "rig rm: aborted, nothing changed")
			return 1
		}
	}

	backup, err := st.Remove(p, dbc)
	if err != nil {
		fmt.Fprintf(stderr, "rig rm: %v\n", err)
		return 1
	}
	if *asJSON {
		writeJSON(stdout, map[string]any{"name": name, "backup": backup, "purged": p.Purge})
		return 0
	}
	if backup != "" {
		fmt.Fprintln(stdout, "\nDatabase backup:", backup)
	}
	fmt.Fprintf(stdout, "Removed %s.\n", name)
	return 0
}

func renderRmPreview(w io.Writer, p stack.RmPreview) {
	dbName, _ := db.Name(p.Name)
	fmt.Fprintf(w, "rig rm %s will:\n\n", p.Name)
	fmt.Fprintf(w, "  - stop serving https://%s.localhost (nginx config and certificate removed)\n", p.Name)
	if p.DBExists {
		fmt.Fprintf(w, "  - back up the database %s to ~/.local/share/rig/backups, then drop it and its user\n", dbName)
	} else {
		fmt.Fprintf(w, "  - remove the database user %s (there is no database)\n", dbName)
	}
	if p.Purge {
		fmt.Fprintf(w, "  - DELETE the project folder %s and everything in it\n", p.Dir)
		for _, warning := range p.Warnings {
			fmt.Fprintf(w, "\n  WARNING: %s\n", warning)
		}
	} else {
		fmt.Fprintf(w, "  - keep the project folder %s\n", p.Dir)
	}
}

// confirmName asks the user to type the site name to confirm deletion.
func confirmName(stdout io.Writer, name string) bool {
	fmt.Fprintf(stdout, "\nType the site name (%s) to delete it: ", name)
	line, _ := bufio.NewReader(stdin).ReadString('\n')
	return strings.TrimSpace(line) == name
}
