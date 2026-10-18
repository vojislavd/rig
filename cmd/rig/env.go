package main

import (
	"flag"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/vojislavd/rig/internal/db"
	"github.com/vojislavd/rig/internal/site"
	"github.com/vojislavd/rig/internal/stack"
)

// runEnv handles "rig env [name] [--yes] [--json]".
func runEnv(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("env", flag.ContinueOnError)
	fs.SetOutput(stderr)
	yes := fs.Bool("yes", false, "apply without asking")
	asJSON := fs.Bool("json", false, "machine-readable output (needs --yes)")
	noDB := fs.Bool("no-db", false, "only set APP_URL; don't create or touch a database")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	name := ""
	if fs.NArg() > 0 {
		name = fs.Arg(0)
		if err := fs.Parse(fs.Args()[1:]); err != nil {
			return 2
		}
		if fs.NArg() > 0 {
			fmt.Fprintf(stderr, "rig env: unexpected argument %q\n", fs.Arg(0))
			return 2
		}
	}
	if *asJSON && !*yes {
		fmt.Fprintln(stderr, "rig env: --json needs --yes (there is no prompt in JSON mode)")
		return 2
	}

	st, err := newStack()
	if err != nil {
		fmt.Fprintf(stderr, "rig env: %v\n", err)
		return 1
	}
	if name == "" {
		if name, err = currentSite(st); err != nil {
			fmt.Fprintf(stderr, "rig env: %v\n", err)
			return 1
		}
	}
	r, err := site.Load(st.L.Registry())
	if err != nil {
		fmt.Fprintf(stderr, "rig env: %v\n", err)
		return 1
	}
	if _, ok := r.Get(name); !ok {
		fmt.Fprintf(stderr, "rig env: %q is not a linked site (run rig link first)\n", name)
		return 1
	}
	var dbc stack.DBCreator
	if !*noDB {
		c, err := newDBClient()
		if err != nil {
			fmt.Fprintf(stderr, "rig env: %v\n", err)
			return 1
		}
		dbc = c
	}

	confirmFn := func(p stack.EnvPreview) bool {
		if *asJSON {
			return true
		}
		renderEnvPreview(stdout, name, p, dbc != nil)
		return *yes || confirm(stdout)
	}
	preview, applied, err := st.Env(name, dbc, confirmFn)
	if err != nil {
		fmt.Fprintf(stderr, "rig env: %v\n", err)
		return 1
	}
	if *asJSON {
		writeJSON(stdout, map[string]any{"site": name, "applied": applied, "preview": preview})
		return 0
	}
	if !applied {
		fmt.Fprintln(stderr, "rig env: aborted, nothing changed")
		return 1
	}
	fmt.Fprintf(stdout, "\nUpdated %s (mode 0600).\n", preview.Path)
	if preview.FromExample {
		fmt.Fprintln(stdout, "It was created from .env.example: run php artisan key:generate in the project.")
	}
	return 0
}

// renderEnvPreview prints the planned .env changes. Secrets were already
// replaced with "(hidden)" by the env package.
func renderEnvPreview(w io.Writer, name string, p stack.EnvPreview, withDB bool) {
	if p.FromExample {
		fmt.Fprintf(w, "%s does not exist yet; it will be created from .env.example.\n", p.Path)
	}
	fmt.Fprintf(w, "rig env will change %s:\n\n", p.Path)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, c := range p.Changes {
		old := c.Old
		switch c.Before {
		case "missing":
			old = "(missing)"
		case "commented":
			old = "(commented out)"
		}
		newVal := c.New
		if newVal == "(hidden)" {
			newVal = "(secret, hidden)"
		}
		fmt.Fprintf(tw, "  %s\t%s\t->\t%s\n", c.Key, old, newVal)
	}
	if p.OldMode != "" {
		fmt.Fprintf(tw, "  file mode\t%s\t->\t0600\n", p.OldMode)
	}
	tw.Flush()
	if withDB {
		dbName, _ := db.Name(name)
		fmt.Fprintf(w, "\nThis also creates the database %s if needed and sets a new password for its user.\n", dbName)
	}
}
