package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"text/tabwriter"

	"github.com/vojislavd/rig/internal/site"
	"github.com/vojislavd/rig/internal/stack"
	"github.com/vojislavd/rig/internal/worker"
)

var workerUsage = `usage: rig worker <command>

Workers run only when you start them: never at login, and logging out
stops them.

commands:
  start <site> <kind>     start a worker in the background
  stop <site> [kind]      stop one worker, or all of the site's workers
  stop --all              stop every worker
  remove <site> <kind>    stop it; for reverb also release its port and nginx route
  restart <site> <kind>   restart (needed after code changes)
  logs <site> <kind>      follow a worker's log (Ctrl+C to stop)
  list                    list running workers

kinds: ` + strings.Join(worker.KindNames(), ", ") + "\n"

// runWorker handles "rig worker ...".
func runWorker(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, workerUsage)
		return 2
	}
	sub := args[0]
	fs := flag.NewFlagSet("worker "+sub, flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "machine-readable output")
	all := fs.Bool("all", false, "stop: every worker of every site")
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

	// Check the arguments' shape before touching anything.
	switch {
	case sub == "list":
	case sub == "stop" && *all && len(pos) == 0:
	case sub == "stop" && !*all && (len(pos) == 1 || len(pos) == 2):
	case (sub == "start" || sub == "restart" || sub == "logs" || sub == "remove") && len(pos) == 2:
	case sub != "start" && sub != "stop" && sub != "restart" && sub != "logs" && sub != "remove":
		fmt.Fprintf(stderr, "rig worker: unknown command %q\n\n%s", sub, workerUsage)
		return 2
	default:
		fmt.Fprint(stderr, workerUsage)
		return 2
	}

	st, err := newStack()
	if err != nil {
		fmt.Fprintf(stderr, "rig worker: %v\n", err)
		return 1
	}

	switch sub {
	case "list":
		list, err := st.Workers()
		if err != nil {
			fmt.Fprintf(stderr, "rig worker: %v\n", err)
			return 1
		}
		if *asJSON {
			if list == nil {
				list = []stack.WorkerInfo{}
			}
			writeJSON(stdout, list)
			return 0
		}
		if len(list) == 0 {
			fmt.Fprintln(stdout, "No workers running. Start one with: rig worker start <site> <kind>")
			return 0
		}
		tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "SITE\tWORKER\tSTATE")
		for _, w := range list {
			fmt.Fprintf(tw, "%s\t%s\t%s\n", w.Site, w.Kind, w.State)
		}
		tw.Flush()
		return 0

	case "logs":
		name, kind := pos[0], pos[1]
		if err := site.ValidateName(name); err != nil {
			fmt.Fprintf(stderr, "rig worker: %v\n", err)
			return 1
		}
		if err := worker.CheckKind(kind); err != nil {
			fmt.Fprintf(stderr, "rig worker: %v\n", err)
			return 1
		}
		// Hand the terminal to journalctl. The unit name is built only
		// from a validated site name and a known kind.
		cmd := exec.Command("/usr/bin/journalctl", "--user", "-u", worker.UnitName(name, kind), "-n", "50", "-f")
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(stderr, "rig worker: %v\n", err)
			return 1
		}
		return 0
	}

	var msg string
	switch {
	case sub == "start":
		err = st.StartWorker(pos[0], pos[1])
		msg = fmt.Sprintf("Started the %s worker for %s. Stop it with: rig worker stop %s", pos[1], pos[0], pos[0])
		if pos[1] == "reverb" {
			msg += fmt.Sprintf("\n\nThe browser reaches it at wss://%s/app/... through nginx.\n"+
				"First time? Run rig env %s (sets REVERB_* in .env), then npm run build\n"+
				"in the project: Vite builds the VITE_REVERB_* values into the JavaScript.", site.Host(pos[0]), pos[0])
		}
	case sub == "remove" && pos[1] == "reverb":
		err = st.RemoveReverb(pos[0])
		msg = fmt.Sprintf("Removed Reverb from %s (port and nginx route released).", pos[0])
	case sub == "remove":
		err = st.StopWorkers(pos[0], pos[1])
		msg = fmt.Sprintf("Stopped the %s worker for %s.", pos[1], pos[0])
	case sub == "restart":
		err = st.RestartWorker(pos[0], pos[1])
		msg = fmt.Sprintf("Restarted the %s worker for %s.", pos[1], pos[0])
	case sub == "stop" && *all:
		err = st.StopAllWorkers()
		msg = "Stopped all workers."
	case sub == "stop" && len(pos) == 2:
		err = st.StopWorkers(pos[0], pos[1])
		msg = fmt.Sprintf("Stopped the %s worker for %s.", pos[1], pos[0])
	default: // stop <site>
		err = st.StopWorkers(pos[0], "")
		msg = fmt.Sprintf("Stopped all workers for %s.", pos[0])
	}
	if err != nil {
		fmt.Fprintf(stderr, "rig worker: %v\n", err)
		return 1
	}
	if *asJSON {
		writeJSON(stdout, map[string]any{"command": sub, "args": pos, "ok": true})
		return 0
	}
	fmt.Fprintln(stdout, msg)
	return 0
}
