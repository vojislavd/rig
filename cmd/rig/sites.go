package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"text/tabwriter"

	"github.com/vojislavd/rig/internal/site"
	"github.com/vojislavd/rig/internal/stack"
)

// newStack is a variable so tests can use a temporary Stack.
var newStack = stack.New

// siteCommand parses "[name] [--json]" for link, unlink and open. Go's
// flag package stops at the first non-flag argument, so flags are parsed
// again after the name to allow both "rig link foo --json" and
// "rig link --json foo".
func siteCommand(cmd string, args []string, stderr io.Writer) (name string, asJSON, noDB bool, code int) {
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	fs.SetOutput(stderr)
	jsonOut := fs.Bool("json", false, "machine-readable output")
	var noDBFlag *bool
	if cmd == "link" {
		noDBFlag = fs.Bool("no-db", false, "don't create a database; only set APP_URL in .env")
	}
	if err := fs.Parse(args); err != nil {
		return "", false, false, 2
	}
	rest := fs.Args()
	if len(rest) > 0 {
		name = rest[0]
		if err := fs.Parse(rest[1:]); err != nil {
			return "", false, false, 2
		}
		if fs.NArg() > 0 {
			fmt.Fprintf(stderr, "rig %s: unexpected argument %q\n", cmd, fs.Arg(0))
			return "", false, false, 2
		}
	}
	return name, *jsonOut, noDBFlag != nil && *noDBFlag, -1
}

// currentSite returns the site name for the current folder, used when a
// command is run inside a project without a name.
func currentSite(st *stack.Stack) (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	dir, err := site.SiteDir(st.L.Code, cwd)
	if err != nil {
		return "", fmt.Errorf("not inside a site folder (%w); give the site name", err)
	}
	return filepath.Base(dir), nil
}

func writeJSON(w io.Writer, v any) {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.Encode(v)
}

// runLink handles "rig link [name] [--json]" in the project folder.
func runLink(args []string, stdout, stderr io.Writer) int {
	name, asJSON, noDB, code := siteCommand("link", args, stderr)
	if code >= 0 {
		return code
	}
	st, err := newStack()
	if err != nil {
		fmt.Fprintf(stderr, "rig link: %v\n", err)
		return 1
	}
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(stderr, "rig link: %v\n", err)
		return 1
	}
	s, err := st.Link(cwd, name)
	if err != nil {
		fmt.Fprintf(stderr, "rig link: %v\n", err)
		return 1
	}
	url := "https://" + site.Host(s.Name)
	if asJSON {
		writeJSON(stdout, map[string]string{"name": s.Name, "url": url, "dir": s.Dir})
		return 0
	}
	fmt.Fprintf(stdout, "Linked %s: %s\n\n", s.Name, url)

	// Offer the database and .env step right away. Declining is fine:
	// the site stays linked and rig env can run later.
	var dbc stack.DBCreator
	if !noDB {
		c, err := newDBClient()
		if err != nil {
			fmt.Fprintf(stdout, "Skipping the database: %v\n", err)
			return 0
		}
		dbc = c
	}
	_, applied, err := st.Env(s.Name, dbc, func(p stack.EnvPreview) bool {
		renderEnvPreview(stdout, s.Name, p, dbc != nil)
		return confirm(stdout)
	})
	switch {
	case err != nil:
		fmt.Fprintf(stderr, "rig link: site linked, but the database/.env step failed: %v\n", err)
		return 1
	case applied && dbc == nil:
		fmt.Fprintln(stdout, "\n.env updated (APP_URL only).")
	case applied:
		fmt.Fprintf(stdout, "\n.env updated. Run php artisan migrate in the project when ready.\n")
	default:
		fmt.Fprintln(stdout, "\nDatabase and .env left alone; run rig env later.")
	}
	return 0
}

// runUnlink handles "rig unlink [name] [--json]".
func runUnlink(args []string, stdout, stderr io.Writer) int {
	name, asJSON, _, code := siteCommand("unlink", args, stderr)
	if code >= 0 {
		return code
	}
	st, err := newStack()
	if err != nil {
		fmt.Fprintf(stderr, "rig unlink: %v\n", err)
		return 1
	}
	if name == "" {
		if name, err = currentSite(st); err != nil {
			fmt.Fprintf(stderr, "rig unlink: %v\n", err)
			return 1
		}
	}
	if err := st.Unlink(name); err != nil {
		fmt.Fprintf(stderr, "rig unlink: %v\n", err)
		return 1
	}
	if asJSON {
		writeJSON(stdout, map[string]string{"name": name, "unlinked": "true"})
		return 0
	}
	fmt.Fprintf(stdout, "Unlinked %s. Code and database were not touched.\n", name)
	return 0
}

// runSites handles "rig sites [--json]".
func runSites(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("sites", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "machine-readable output")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	st, err := newStack()
	if err != nil {
		fmt.Fprintf(stderr, "rig sites: %v\n", err)
		return 1
	}
	list, err := st.Sites()
	if err != nil {
		fmt.Fprintf(stderr, "rig sites: %v\n", err)
		return 1
	}
	if *asJSON {
		if list == nil {
			list = []stack.SiteInfo{} // print [] rather than null
		}
		writeJSON(stdout, list)
		return 0
	}
	if len(list) == 0 {
		fmt.Fprintln(stdout, "No sites yet. Run rig link inside a project in ~/code.")
		return 0
	}
	// tabwriter lines up columns.
	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "SITE\tURL\tCERTIFICATE")
	for _, s := range list {
		cert := "valid until " + s.CertExpiry.Format("2006-01-02")
		if s.CertIssue != "" {
			cert = "needs renewal: " + s.CertIssue
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\n", s.Name, s.URL, cert)
	}
	tw.Flush()
	return 0
}

// runOpen handles "rig open [name]": opens the site in the browser.
func runOpen(args []string, stdout, stderr io.Writer) int {
	name, _, _, code := siteCommand("open", args, stderr)
	if code >= 0 {
		return code
	}
	st, err := newStack()
	if err != nil {
		fmt.Fprintf(stderr, "rig open: %v\n", err)
		return 1
	}
	if name == "" {
		if name, err = currentSite(st); err != nil {
			fmt.Fprintf(stderr, "rig open: %v\n", err)
			return 1
		}
	}
	r, err := site.Load(st.L.Registry())
	if err != nil {
		fmt.Fprintf(stderr, "rig open: %v\n", err)
		return 1
	}
	if _, ok := r.Get(name); !ok {
		fmt.Fprintf(stderr, "rig open: %q is not a linked site\n", name)
		return 1
	}
	url := "https://" + site.Host(name)
	// The URL is built from a validated, registered site name only.
	if err := exec.Command("/usr/bin/xdg-open", url).Start(); err != nil {
		fmt.Fprintf(stderr, "rig open: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, "Opening", url)
	return 0
}
