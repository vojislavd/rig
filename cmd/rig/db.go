package main

import (
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/vojislavd/rig/internal/db"
	"github.com/vojislavd/rig/internal/paths"
)

// newDBClient is a variable so tests can avoid the real server.
var newDBClient = func() (*db.Client, error) {
	cfg, err := paths.ConfigDir()
	if err != nil {
		return nil, err
	}
	backups, err := paths.BackupsDir()
	if err != nil {
		return nil, err
	}
	cnf := filepath.Join(cfg, "mariadb.cnf")
	// Refuse a missing or non-private login file before using it.
	if _, err := db.LoadAdmin(cnf); err != nil {
		return nil, fmt.Errorf("MariaDB login %s: %w (run rig install first)", cnf, err)
	}
	return &db.Client{Cnf: cnf, Backups: backups, Now: time.Now}, nil
}

const dbUsage = `usage: rig db <command>

commands:
  list          list rig site databases
  dump <site>   write a gzipped backup to ~/.local/share/rig/backups
  drop <site>   back up, then drop the site's database and user (site stays linked)
`

// runDB handles "rig db list|dump".
func runDB(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, dbUsage)
		return 2
	}
	sub, rest := args[0], args[1:]
	fs := flag.NewFlagSet("db "+sub, flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "machine-readable output")
	yes := fs.Bool("yes", false, "don't ask (drop only)")
	// Allow flags before and after the site name ("drop foo --yes").
	if err := fs.Parse(rest); err != nil {
		return 2
	}
	var positional []string
	for fs.NArg() > 0 {
		positional = append(positional, fs.Arg(0))
		if err := fs.Parse(fs.Args()[1:]); err != nil {
			return 2
		}
	}

	switch sub {
	case "list":
		c, err := newDBClient()
		if err != nil {
			fmt.Fprintf(stderr, "rig db: %v\n", err)
			return 1
		}
		names, err := c.List()
		if err != nil {
			fmt.Fprintf(stderr, "rig db: %v\n", err)
			return 1
		}
		if *asJSON {
			if names == nil {
				names = []string{}
			}
			writeJSON(stdout, names)
			return 0
		}
		if len(names) == 0 {
			fmt.Fprintln(stdout, "No rig databases yet.")
		}
		for _, n := range names {
			fmt.Fprintln(stdout, n)
		}
		return 0
	case "dump":
		if len(positional) != 1 {
			fmt.Fprint(stderr, dbUsage)
			return 2
		}
		c, err := newDBClient()
		if err != nil {
			fmt.Fprintf(stderr, "rig db: %v\n", err)
			return 1
		}
		path, err := c.Dump(positional[0])
		if err != nil {
			fmt.Fprintf(stderr, "rig db: %v\n", err)
			return 1
		}
		if *asJSON {
			writeJSON(stdout, map[string]string{"backup": path})
			return 0
		}
		fmt.Fprintln(stdout, "Backup written:", path)
		return 0
	case "drop":
		if len(positional) != 1 {
			fmt.Fprint(stderr, dbUsage)
			return 2
		}
		if *asJSON && !*yes {
			fmt.Fprintln(stderr, "rig db drop: --json needs --yes")
			return 2
		}
		siteName := positional[0]
		dbName, err := db.Name(siteName)
		if err != nil {
			fmt.Fprintf(stderr, "rig db: %v\n", err)
			return 1
		}
		c, err := newDBClient()
		if err != nil {
			fmt.Fprintf(stderr, "rig db: %v\n", err)
			return 1
		}
		if !*asJSON {
			fmt.Fprintf(stdout, "rig db drop will back up %s to ~/.local/share/rig/backups, then drop the database and its user.\nThe site stays linked; its .env keeps the old DB_* lines.\n", dbName)
		}
		if !*yes && !confirm(stdout) {
			fmt.Fprintln(stderr, "rig db drop: aborted, nothing changed")
			return 1
		}
		backup, err := c.DropSite(siteName)
		if err != nil {
			fmt.Fprintf(stderr, "rig db: %v\n", err)
			return 1
		}
		if *asJSON {
			writeJSON(stdout, map[string]string{"dropped": dbName, "backup": backup})
			return 0
		}
		if backup != "" {
			fmt.Fprintln(stdout, "\nBackup written:", backup)
		}
		fmt.Fprintf(stdout, "Dropped %s.\n", dbName)
		return 0
	default:
		fmt.Fprintf(stderr, "rig db: unknown command %q\n\n%s", sub, dbUsage)
		return 2
	}
}
