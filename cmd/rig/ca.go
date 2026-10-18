package main

import (
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/vojislavd/rig/internal/install"
)

// runCA handles "rig ca rotate [--dry-run]".
func runCA(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "rotate" {
		fmt.Fprintln(stderr, "usage: rig ca rotate [--dry-run]")
		return 2
	}
	fs := flag.NewFlagSet("ca rotate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dryRun := fs.Bool("dry-run", false, "show the changes without applying them")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "rig ca rotate: unexpected argument %q\n", fs.Arg(0))
		return 2
	}
	fail := func(err error) int {
		fmt.Fprintf(stderr, "rig ca rotate: %v\n", err)
		return 1
	}

	st, err := newStack()
	if err != nil {
		return fail(err)
	}
	pending, isNew, err := st.PendingCA()
	if err != nil {
		return fail(fmt.Errorf("pending CA in %s.new: %w (delete that folder to start over)", st.L.CA, err))
	}
	if !isNew {
		fmt.Fprintf(stdout, "Resuming: using the new CA already waiting in %s.new.\n\n", st.L.CA)
	}

	plan := install.BuildTrust(pending.Cert.Raw)
	if err := plan.Validate(); err != nil {
		return fail(fmt.Errorf("invalid plan: %w", err))
	}
	fmt.Fprintf(stdout, "rig ca rotate replaces rig's CA. The new one is valid until %s.\n\n", pending.Cert.NotAfter.Format(time.DateOnly))
	fmt.Fprintf(stdout, "First, as you: save the new CA in %s.new.\n\n", st.L.CA)
	plan.Render(stdout, false)
	fmt.Fprintf(stdout, "\nThen, as you: delete the old CA in %s so its key can never sign\n", st.L.CA)
	fmt.Fprintln(stdout, "again, issue new certificates for every site and reload nginx. Sites show a")
	fmt.Fprintln(stdout, "certificate error for a few seconds in between.")
	if *dryRun {
		return 0
	}
	if !confirm(stdout) {
		fmt.Fprintln(stderr, "rig ca rotate: aborted, nothing changed")
		return 1
	}

	if isNew {
		if err := st.SavePendingCA(pending); err != nil {
			return fail(err)
		}
	}
	if err := newApplier(stderr).Apply(plan); err != nil {
		return fail(fmt.Errorf("%w\nThe old CA is still in use. Run rig ca rotate again to retry with the same new CA", err))
	}
	if err := st.SwapCA(); err != nil {
		return fail(fmt.Errorf("%w\nThe new CA is trusted but not yet in use; run rig ca rotate again", err))
	}
	changes, err := st.Repair()
	for _, c := range changes {
		fmt.Fprintln(stdout, "done:", c)
	}
	if err != nil {
		return fail(fmt.Errorf("%w\nThe new CA is in place; run rig doctor --fix to finish the certificates", err))
	}
	fmt.Fprintln(stdout, "\nrig ca rotate: done. Restart your browsers so they load the new trust store.")
	return 0
}
