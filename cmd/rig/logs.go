package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strconv"

	"github.com/vojislavd/rig/internal/stack"
)

// runLogs handles "rig logs <target> [-n N] [--no-follow]".
func runLogs(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("logs", flag.ContinueOnError)
	fs.SetOutput(stderr)
	lines := fs.Int("n", 50, "how many lines to show first")
	noFollow := fs.Bool("no-follow", false, "print the lines and exit instead of following")
	// The target may come before the flags ("rig logs foo -n 100"), so
	// take it off first when it doesn't look like a flag.
	var target string
	if len(args) > 0 && len(args[0]) > 0 && args[0][0] != '-' {
		target, args = args[0], args[1:]
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if target == "" && fs.NArg() > 0 {
		target = fs.Arg(0)
	} else if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "rig logs: unexpected argument %q\n", fs.Arg(0))
		return 2
	}
	if target == "" {
		fmt.Fprintf(stderr, "usage: rig logs <target> [-n N] [--no-follow]\ntargets: %s\n", stack.LogTargets)
		return 2
	}
	if *lines < 0 || *lines > 100000 {
		fmt.Fprintln(stderr, "rig logs: -n must be between 0 and 100000")
		return 2
	}

	st, err := newStack()
	if err != nil {
		fmt.Fprintf(stderr, "rig logs: %v\n", err)
		return 1
	}
	src, err := st.LogSource(target)
	if err != nil {
		fmt.Fprintf(stderr, "rig logs: %v\n", err)
		return 1
	}

	// No shell: fixed programs by absolute path. "--" ends tail's options,
	// so no file name is ever read as one.
	n := strconv.Itoa(*lines)
	var argv []string
	if src.File != "" {
		argv = []string{"/usr/bin/tail", "-n", n}
		if !*noFollow {
			argv = append(argv, "-F") // follow by name: survives log rotation
		}
		argv = append(argv, "--", src.File)
	} else {
		argv = []string{"/usr/bin/journalctl", "--user", "-u", src.Unit, "-n", n, "--no-pager"}
		if !*noFollow {
			argv = append(argv, "-f")
		}
	}
	if !*noFollow {
		fmt.Fprintf(stderr, "Following %s (Ctrl+C to stop)\n", describe(src))
	}

	// Ctrl+C: rig catches it (ignoring it instead would be inherited by
	// the child) and passes it on, so the log program stops even when
	// only rig got the signal, and rig exits cleanly.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	defer signal.Stop(sig)

	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdout, cmd.Stderr = stdout, stderr
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(stderr, "rig logs: %v\n", err)
		return 1
	}
	// Wait in the background so this loop can pass on signals meanwhile.
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	interrupted := false
	for waiting := true; waiting; {
		select {
		case <-sig:
			interrupted = true
			cmd.Process.Signal(os.Interrupt)
		case err = <-done:
			waiting = false
		}
	}
	if interrupted {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return 1 // the program already printed why
	}
	if err != nil {
		fmt.Fprintf(stderr, "rig logs: %v\n", err)
		return 1
	}
	return 0
}

func describe(src stack.LogSource) string {
	if src.File != "" {
		return src.File
	}
	return "journal of " + src.Unit
}
