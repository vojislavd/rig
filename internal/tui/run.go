package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/vojislavd/rig/internal/stack"
)

// Run shows the dashboard until the user quits. rigPath is the rig
// binary; every action runs it, so all logic stays in the CLI.
func Run(rigPath string) (err error) {
	in, out := int(os.Stdin.Fd()), os.Stdout
	restore, err := rawMode(in)
	if err != nil {
		return err
	}
	fmt.Fprint(out, altScreenOn+cursorHide)
	// Always give the terminal back, even after a panic.
	defer func() {
		fmt.Fprint(out, cursorShow+altScreenOff)
		restore()
		if r := recover(); r != nil {
			err = fmt.Errorf("dashboard crashed: %v", r)
		}
	}()

	keys := make(chan []Key)
	go func() {
		buf := make([]byte, 32)
		for {
			n, err := os.Stdin.Read(buf)
			if err != nil {
				close(keys)
				return
			}
			keys <- parseKeys(buf[:n])
		}
	}()
	resize := make(chan os.Signal, 1)
	signal.Notify(resize, syscall.SIGWINCH)
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(resize)
	defer signal.Stop(stop)
	tick := time.NewTicker(3 * time.Second)
	defer tick.Stop()

	_, noColor := os.LookupEnv("NO_COLOR")
	s := screen{color: !noColor}
	refresh := func() {
		rep, err := status(rigPath)
		if err != nil {
			s.message, s.messageOK = "status: "+err.Error(), false
			return
		}
		s.rep = rep
		if s.sel >= len(rep.Sites) {
			s.sel = max(0, len(rep.Sites)-1)
		}
	}
	draw := func() {
		w, h := size(in)
		s.width, s.now = w, time.Now()
		lines := render(s)
		if len(lines) > h {
			lines = lines[:h]
		}
		fmt.Fprint(out, clearScreen+strings.Join(lines, "\r\n"))
	}

	refresh()
	draw()
	for {
		select {
		case ks, ok := <-keys:
			if !ok {
				return nil
			}
			for _, k := range ks {
				switch k {
				case KeyQuit:
					return nil
				case KeyUp:
					s.sel = max(0, s.sel-1)
				case KeyDown:
					s.sel = min(len(s.rep.Sites)-1, s.sel+1)
				case KeyRefresh:
					refresh()
				default:
					if args, ok := actionFor(k, s.rep, s.sel); ok {
						s.message, s.messageOK, s.pending = "rig "+strings.Join(args, " ")+" ...", true, true
						draw()
						s.message, s.messageOK = runRig(rigPath, args)
						s.pending = false
						refresh()
					}
				}
			}
		case <-tick.C:
			refresh()
		case <-resize:
		case <-stop:
			return nil
		}
		draw()
	}
}

// status runs "rig status --json" and parses it.
func status(rigPath string) (stack.Report, error) {
	var rep stack.Report
	out, err := exec.Command(rigPath, "status", "--json").Output()
	if err != nil {
		return rep, err
	}
	return rep, json.Unmarshal(out, &rep)
}

// runRig runs one rig command without a terminal (no prompts) and returns
// a one-line summary of what happened, and whether it succeeded.
func runRig(rigPath string, args []string) (string, bool) {
	cmd := exec.Command(rigPath, args...)
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	err := cmd.Run()
	last := ""
	for _, l := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if strings.TrimSpace(l) != "" {
			last = strings.TrimSpace(l)
			break // the first line says what happened
		}
	}
	if err != nil && last == "" {
		last = err.Error()
	}
	return "rig " + strings.Join(args, " ") + ": " + last, err == nil
}
