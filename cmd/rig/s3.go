package main

import (
	"flag"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/vojislavd/rig/internal/garage"
	"github.com/vojislavd/rig/internal/site"
	"github.com/vojislavd/rig/internal/stack"
)

const s3Usage = `usage: rig s3 <command>

Garage stores files for sites using the s3 disk. It runs only when
started. While stopped, uploads and downloads fail.

commands:
  start                        start Garage (S3 on 127.0.0.1:3900)
  stop                         stop Garage (stored files are kept)
  status                       show whether it runs
  enable <site>                give the site a bucket and point its .env at it
  ls <site> [folder]           list the site's files
  get <site> <file> [dest]     download (--force replaces an existing file)
  put <site> <local> <file>    upload a local file
  rm <site> <file>             delete a file (asks first; --yes skips)
`

// runS3 handles "rig s3 ...".
func runS3(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, s3Usage)
		return 2
	}
	sub := args[0]
	fs := flag.NewFlagSet("s3 "+sub, flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "machine-readable output (ls)")
	force := fs.Bool("force", false, "get: replace an existing local file")
	yes := fs.Bool("yes", false, "rm: don't ask")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	var pos []string
	for fs.NArg() > 0 {
		pos = append(pos, fs.Arg(0))
		if err := fs.Parse(fs.Args()[1:]); err != nil {
			return 2
		}
	}

	// Check the shape of the arguments before touching anything.
	ok := map[string]bool{
		"start": len(pos) == 0, "stop": len(pos) == 0, "status": len(pos) == 0,
		"enable": len(pos) == 1,
		"ls":     len(pos) == 1 || len(pos) == 2,
		"get":    len(pos) == 2 || len(pos) == 3,
		"put":    len(pos) == 3,
		"rm":     len(pos) == 2,
	}
	valid, known := ok[sub]
	if !known {
		fmt.Fprintf(stderr, "rig s3: unknown command %q\n\n%s", sub, s3Usage)
		return 2
	}
	if !valid {
		fmt.Fprint(stderr, s3Usage)
		return 2
	}

	st, err := newStack()
	if err != nil {
		fmt.Fprintf(stderr, "rig s3: %v\n", err)
		return 1
	}
	fail := func(err error) int {
		fmt.Fprintf(stderr, "rig s3: %v\n", err)
		return 1
	}

	switch sub {
	case "start":
		if err := st.StartS3(); err != nil {
			return fail(err)
		}
		fmt.Fprintln(stdout, "Garage is running (rig s3 stop when done).")
	case "stop":
		if err := st.StopS3(); err != nil {
			return fail(err)
		}
		fmt.Fprintln(stdout, "Garage stopped. Stored files are kept.")
	case "status":
		fmt.Fprintf(stdout, "Garage: %s\n", st.S3State())
	case "enable":
		return s3Enable(st, pos[0], stdout, stderr)
	case "ls":
		prefix := ""
		if len(pos) == 2 {
			prefix = pos[1]
		}
		objs, err := st.S3List(pos[0], prefix)
		if err != nil {
			return fail(err)
		}
		if *asJSON {
			if objs == nil {
				objs = []garage.Object{}
			}
			writeJSON(stdout, objs)
			return 0
		}
		if len(objs) == 0 {
			fmt.Fprintln(stdout, "No files.")
			return 0
		}
		tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', tabwriter.AlignRight)
		var total int64
		for _, o := range objs {
			fmt.Fprintf(tw, "%s\t%s\t  %s\t\n", o.LastModified.Local().Format("2006-01-02 15:04"), humanSize(o.Size), o.Key)
			total += o.Size
		}
		tw.Flush()
		fmt.Fprintf(stdout, "%d file(s), %s\n", len(objs), humanSize(total))
	case "get":
		dest := ""
		if len(pos) == 3 {
			dest = pos[2]
		}
		path, err := st.S3Get(pos[0], pos[1], dest, *force)
		if err != nil {
			return fail(err)
		}
		fmt.Fprintln(stdout, "Downloaded to", path)
	case "put":
		n, err := st.S3Put(pos[0], pos[1], pos[2])
		if err != nil {
			return fail(err)
		}
		fmt.Fprintf(stdout, "Uploaded %s (%s) as %s\n", pos[1], humanSize(n), pos[2])
	case "rm":
		if !*yes {
			if !ask(stdout, fmt.Sprintf("Delete %s from %s's bucket? This can't be undone.", pos[1], pos[0])) {
				fmt.Fprintln(stderr, "rig s3: aborted, nothing deleted")
				return 1
			}
		}
		if err := st.S3Remove(pos[0], pos[1]); err != nil {
			return fail(err)
		}
		fmt.Fprintln(stdout, "Deleted", pos[1])
	}
	return 0
}

// s3Enable gives the site a bucket, then offers the .env changes.
func s3Enable(st *stack.Stack, name string, stdout, stderr io.Writer) int {
	if err := st.EnableS3(name); err != nil {
		fmt.Fprintf(stderr, "rig s3: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "%s has bucket %s at https://%s.\n\n", name, site.Bucket(name), site.FilesHost(name))
	_, applied, err := st.Env(name, nil, func(p stack.EnvPreview) bool {
		renderEnvPreview(stdout, name, p, false)
		return confirm(stdout)
	})
	switch {
	case err != nil:
		fmt.Fprintf(stderr, "rig s3: bucket ready, but updating .env failed: %v\n", err)
		return 1
	case applied:
		fmt.Fprintln(stdout, "\n.env updated: the default disk is now s3.")
	default:
		fmt.Fprintln(stdout, "\n.env left alone; run rig env later (with Garage running).")
	}
	return 0
}

// humanSize formats a byte count: 512 B, 24.1 KB, 3.2 MB.
func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}
