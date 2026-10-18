package main

import (
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/vojislavd/rig/internal/stack"
)

// runStatus handles "rig status [--json]": one overview of everything.
func runStatus(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "machine-readable output")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "rig status: unexpected argument %q\n", fs.Arg(0))
		return 2
	}
	st, err := newStack()
	if err != nil {
		fmt.Fprintf(stderr, "rig status: %v\n", err)
		return 1
	}
	// Databases are listed if rig's MariaDB login works; otherwise "?".
	var dbList func() ([]string, error)
	if c, err := newDBClient(); err == nil {
		dbList = c.List
	}
	rep, err := st.Status(dbList)
	if err != nil {
		fmt.Fprintf(stderr, "rig status: %v\n", err)
		return 1
	}
	if *asJSON {
		if rep.Sites == nil {
			rep.Sites = []stack.SiteStatus{}
		}
		writeJSON(stdout, rep)
		return 0
	}
	renderStatus(stdout, rep)
	return 0
}

func renderStatus(w io.Writer, rep stack.Report) {
	mark := func(ok bool) string {
		if ok {
			return "ok"
		}
		return "MISSING"
	}
	fmt.Fprintf(w, "System    port redirect: %s   CA trusted: %s\n", rep.Install.PortRedirect, mark(rep.Install.CATrusted))

	var svc []string
	for _, s := range rep.Services {
		svc = append(svc, s.Name+": "+s.State)
	}
	fmt.Fprintf(w, "Services  %s\n\n", strings.Join(svc, "   "))

	if len(rep.Sites) == 0 {
		fmt.Fprintln(w, "No sites yet. Run rig link inside a project in ~/code.")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "SITE\tURL\tCERT\tDATABASE\tS3\tREVERB\tWORKERS")
	for _, s := range rep.Sites {
		cert := s.CertExpiry.Format("2006-01-02")
		if s.CertIssue != "" {
			cert = "RENEW: " + s.CertIssue
		}
		dbName := s.Database
		if dbName == "" {
			dbName = "-"
		}
		s3 := "-"
		if s.S3 {
			s3 = "yes"
		}
		reverb := "-"
		if s.ReverbPort != 0 {
			reverb = strconv.Itoa(s.ReverbPort)
		}
		workers := "-"
		if len(s.Workers) > 0 {
			workers = strings.Join(s.Workers, ",")
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", s.Name, s.URL, cert, dbName, s3, reverb, workers)
	}
	tw.Flush()
}
