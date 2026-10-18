// Command rig is a personal local development environment manager.
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/vojislavd/rig/internal/tui"
)

// version is the rig release. Overridden at build time later.
const version = "0.0.0-dev"

const usage = `usage: rig <command>
       rig              (in a terminal: open the dashboard)

commands:
  status      overview: system, services, sites
  doctor      check everything; --fix rewrites rig's files and renews certs
  logs        show and follow a log: nginx, php, mail, s3, <site>, <site>:queue
  link        serve the current project at https://<folder>.localhost
  unlink      stop serving a site (code is never touched)
  rm          unlink + back up and drop the database (--purge: delete code too)
  sites       list linked sites
  open        open a site in the browser
  worker      start/stop queue, schedule or reverb workers while testing
  mail        start/stop Mailpit, which catches all outgoing mail
  s3          start/stop Garage, the S3 storage for sites' files
  env         point the project's .env at its database and URL
  db          list or back up site databases
  ca rotate   replace rig's CA and reissue every site certificate
  install     one-time system setup (use --dry-run to preview)
  uninstall   undo rig install (use --dry-run to preview)
  version     print the rig version
  help        show this help
`

func main() {
	// Security: rig never runs as root. It calls
	// sudo itself for the few actions that need it, so "sudo rig ..." is
	// always a mistake, and refusing it keeps a replaced rig binary from
	// ever running with root rights through us.
	if os.Geteuid() == 0 {
		fmt.Fprintln(os.Stderr, "rig: do not run rig as root or with sudo; it asks for sudo itself when needed")
		os.Exit(1)
	}

	// "rig" alone in a terminal opens the dashboard; in scripts or pipes
	// it prints the usage as before.
	if len(os.Args) == 1 && tui.IsTerminal(int(os.Stdin.Fd())) && tui.IsTerminal(int(os.Stdout.Fd())) {
		self, err := os.Executable()
		if err == nil {
			err = tui.Run(self)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "rig:", err)
			os.Exit(1)
		}
		return
	}

	// os.Args[0] is the program name; the rest are the user's arguments.
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run holds the real logic and returns the exit code. It takes the
// arguments and output streams as parameters instead of using os.*
// directly, so tests can call it and inspect what it printed.
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}

	switch args[0] {
	case "logs":
		return runLogs(args[1:], stdout, stderr)
	case "ca":
		return runCA(args[1:], stdout, stderr)
	case "doctor":
		return runDoctor(args[1:], stdout, stderr)
	case "status":
		return runStatus(args[1:], stdout, stderr)
	case "link":
		return runLink(args[1:], stdout, stderr)
	case "unlink":
		return runUnlink(args[1:], stdout, stderr)
	case "rm":
		return runRm(args[1:], stdout, stderr)
	case "sites":
		return runSites(args[1:], stdout, stderr)
	case "open":
		return runOpen(args[1:], stdout, stderr)
	case "worker":
		return runWorker(args[1:], stdout, stderr)
	case "s3":
		return runS3(args[1:], stdout, stderr)
	case "mail":
		return runMail(args[1:], stdout, stderr)
	case "env":
		return runEnv(args[1:], stdout, stderr)
	case "db":
		return runDB(args[1:], stdout, stderr)
	case "install":
		return runInstall(args[1:], stdout, stderr)
	case "uninstall":
		return runUninstall(args[1:], stdout, stderr)
	case "version":
		fmt.Fprintln(stdout, "rig", version)
		return 0
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	default:
		fmt.Fprintf(stderr, "rig: unknown command %q\n\n%s", args[0], usage)
		return 2
	}
}
