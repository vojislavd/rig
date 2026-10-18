package main

import (
	"fmt"
	"io"
	"os/exec"

	"github.com/vojislavd/rig/internal/mailpit"
)

const mailUsage = `usage: rig mail <command>

Mailpit catches all mail your sites send; nothing leaves the machine.
It runs only when started. While stopped, sending mail fails.

commands:
  start    start Mailpit (SMTP 127.0.0.1:1025, inbox at ` + mailpit.UIURL + `)
  stop     stop Mailpit (caught mail is kept)
  status   show whether it runs
  open     open the inbox in the browser
`

// runMail handles "rig mail start|stop|status|open".
func runMail(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprint(stderr, mailUsage)
		return 2
	}
	switch args[0] {
	case "start", "stop", "status", "open":
	default:
		fmt.Fprintf(stderr, "rig mail: unknown command %q\n\n%s", args[0], mailUsage)
		return 2
	}
	st, err := newStack()
	if err != nil {
		fmt.Fprintf(stderr, "rig mail: %v\n", err)
		return 1
	}
	switch args[0] {
	case "start":
		if err := st.StartMail(); err != nil {
			fmt.Fprintf(stderr, "rig mail: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "Mailpit is running. Inbox: %s (rig mail stop when done)\n", mailpit.UIURL)
	case "stop":
		if err := st.StopMail(); err != nil {
			fmt.Fprintf(stderr, "rig mail: %v\n", err)
			return 1
		}
		fmt.Fprintln(stdout, "Mailpit stopped. Caught mail is kept for next time.")
	case "status":
		fmt.Fprintf(stdout, "Mailpit: %s\n", st.MailState())
	case "open":
		if st.MailState() != "active" {
			fmt.Fprintln(stderr, "rig mail: Mailpit is not running; start it with rig mail start")
			return 1
		}
		if err := exec.Command("/usr/bin/xdg-open", mailpit.UIURL).Start(); err != nil {
			fmt.Fprintf(stderr, "rig mail: %v\n", err)
			return 1
		}
		fmt.Fprintln(stdout, "Opening", mailpit.UIURL)
	}
	return 0
}
