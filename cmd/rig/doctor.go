package main

import (
	"flag"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/vojislavd/rig/internal/stack"
)

// runDoctor handles "rig doctor [--fix] [--security] [--json]". It exits 1 if any
// check fails, so scripts (and the Omarchy widget) can rely on the code.
func runDoctor(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "machine-readable output")
	fix := fs.Bool("fix", false, "rewrite rig's files and renew certificates, then check again")
	security := fs.Bool("security", false, "also check listeners, file modes and leftovers")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "rig doctor: unexpected argument %q\n", fs.Arg(0))
		return 2
	}
	st, err := newStack()
	if err != nil {
		fmt.Fprintf(stderr, "rig doctor: %v\n", err)
		return 1
	}

	var changes []string
	if *fix {
		changes, err = st.Repair()
		if !*asJSON {
			for _, c := range changes {
				fmt.Fprintln(stdout, "fixed:", c)
			}
			if len(changes) == 0 && err == nil {
				fmt.Fprintln(stdout, "fixed: nothing needed fixing")
			}
			fmt.Fprintln(stdout)
		}
		if err != nil {
			fmt.Fprintf(stderr, "rig doctor --fix: %v\n", err)
			return 1
		}
	}

	var dbCheck func() error
	if c, err := newDBClient(); err == nil {
		dbCheck = func() error { _, err := c.List(); return err }
	}
	checks := st.Doctor(dbCheck)
	if *security {
		checks = append(checks, st.Security()...)
	}

	failed := false
	for _, c := range checks {
		failed = failed || c.Status == stack.Fail
	}
	if *asJSON {
		if changes == nil {
			changes = []string{}
		}
		writeJSON(stdout, map[string]any{"ok": !failed, "fixed": changes, "checks": checks})
	} else {
		renderDoctor(stdout, checks)
	}
	if failed {
		return 1
	}
	return 0
}

// renderDoctor prints problems and notes in full; passing checks are counted
// per area, so a healthy run stays short.
func renderDoctor(w io.Writer, checks []stack.Check) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	var areas []string
	passed := map[string]int{}
	problems, shown := 0, 0
	for _, c := range checks {
		if passed[c.Area] == 0 && !contains(areas, c.Area) {
			areas = append(areas, c.Area)
		}
		if c.Status == stack.OK {
			passed[c.Area]++
			continue
		}
		shown++
		if c.Status != stack.Info {
			problems++
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", strings.ToUpper(c.Status), c.Area, c.Name, c.Detail)
		if c.Fix != "" {
			fmt.Fprintf(tw, "\t\t\tfix: %s\n", c.Fix)
		}
	}
	tw.Flush()
	if shown > 0 {
		fmt.Fprintln(w)
	}
	var sum []string
	for _, a := range areas {
		sum = append(sum, fmt.Sprintf("%s %d ok", a, passed[a]))
	}
	fmt.Fprintln(w, strings.Join(sum, ", "))
	if problems == 0 {
		fmt.Fprintln(w, "All good.")
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
