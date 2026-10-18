package install

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
)

// sudo is called by absolute path, so a fake "sudo" earlier in PATH is
// never used.
const sudo = "/usr/bin/sudo"

// Applier carries out a plan. Its fields are functions so tests can
// replace the parts that touch the real system.
type Applier struct {
	// Run executes argv, feeding stdin (may be nil) to it.
	Run func(argv []string, stdin []byte) error
	// Exists reports whether path exists.
	Exists func(path string) bool
	// Verify checks a written file has exactly the expected content,
	// mode, and root ownership.
	Verify func(path string, content []byte, mode fs.FileMode) error
	// Log receives progress messages.
	Log io.Writer
}

// NewApplier returns an Applier that runs real commands, printing their
// output (and sudo's password prompt) to the terminal.
func NewApplier(log io.Writer) *Applier {
	return &Applier{Run: execRun, Exists: exists, Verify: verifyRootFile, Log: log}
}

// Apply validates p and then performs its actions in order. It stops at
// the first failing action that isn't Optional and returns its error;
// the caller decides about rollback.
//
// Security: rig itself never runs as root. Each action becomes one sudo
// call to a fixed system program:
//   - write:  sudo install -o root -g root -m MODE /dev/stdin PATH
//     The content is streamed through stdin, never left in a temp file
//     another process could swap before root copies it.
//   - run:    sudo PROGRAM ARGS (program on the allowlist)
//   - remove: sudo rm -f -- PATH, sudo rmdir -- PATH
func (a *Applier) Apply(p *Plan) error {
	if err := p.Validate(); err != nil {
		return err
	}
	// Check every program exists before changing anything, so a missing
	// tool can't leave a half-applied plan.
	for _, prog := range p.programs() {
		if !a.Exists(prog) {
			return fmt.Errorf("%s not found; is its package installed?", prog)
		}
	}
	// Ask for the sudo password once, up front. Later calls reuse it.
	if err := a.Run([]string{sudo, "-v"}, nil); err != nil {
		return fmt.Errorf("sudo: %w", err)
	}
	for i, act := range p.Actions {
		fmt.Fprintf(a.Log, "[%d/%d] %s\n", i+1, len(p.Actions), act.Why)
		err := a.do(act)
		if err != nil && act.Optional {
			fmt.Fprintf(a.Log, "      skipped: %v\n", err)
			continue
		}
		if err != nil {
			return fmt.Errorf("step %d (%s): %w", i+1, act.Why, err)
		}
	}
	return nil
}

func (a *Applier) do(act Action) error {
	switch act.Kind {
	case WriteFile:
		dir := filepath.Dir(act.Path)
		if !a.Exists(dir) {
			if !allowedDirs[dir] {
				return fmt.Errorf("folder %s does not exist", dir)
			}
			if err := a.Run([]string{sudo, "/usr/bin/install", "-d", "-o", "root", "-g", "root", "-m", "0755", "--", dir}, nil); err != nil {
				return err
			}
		}
		mode := strconv.FormatUint(uint64(act.Mode.Perm()), 8)
		argv := []string{sudo, "/usr/bin/install", "-o", "root", "-g", "root", "-m", mode, "/dev/stdin", act.Path}
		if err := a.Run(argv, act.Content); err != nil {
			return err
		}
		// Read the file back and confirm it is exactly what we meant.
		return a.Verify(act.Path, act.Content, act.Mode)
	case Run:
		return a.Run(append([]string{sudo}, act.Argv...), act.Stdin)
	case RemoveFile:
		return a.Run([]string{sudo, "/usr/bin/rm", "-f", "--", act.Path}, nil)
	case RemoveDir:
		if !a.Exists(act.Path) {
			return nil
		}
		return a.Run([]string{sudo, "/usr/bin/rmdir", "--", act.Path}, nil)
	}
	return fmt.Errorf("unknown action kind %d", act.Kind)
}

// programs lists every program applying p will run.
func (p *Plan) programs() []string {
	progs := []string{sudo}
	seen := map[string]bool{sudo: true}
	add := func(prog string) {
		if !seen[prog] {
			seen[prog] = true
			progs = append(progs, prog)
		}
	}
	for _, act := range p.Actions {
		switch act.Kind {
		case WriteFile:
			add("/usr/bin/install")
		case Run:
			add(act.Argv[0])
		case RemoveFile:
			add("/usr/bin/rm")
		case RemoveDir:
			add("/usr/bin/rmdir")
		}
	}
	return progs
}

// execRun runs argv directly (no shell). sudo asks for the password on
// the terminal itself, so stdin can carry file content.
func execRun(argv []string, stdin []byte) error {
	cmd := exec.Command(argv[0], argv[1:]...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	} else {
		cmd.Stdin = os.Stdin
	}
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// verifyRootFile confirms path is a regular file owned by root with
// exactly the given mode and content.
func verifyRootFile(path string, content []byte, mode fs.FileMode) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", path)
	}
	if info.Mode().Perm() != mode {
		return fmt.Errorf("%s has mode %04o, want %04o", path, info.Mode().Perm(), mode)
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st.Uid != 0 || st.Gid != 0 {
		return fmt.Errorf("%s is not owned by root", path)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if !bytes.Equal(got, content) {
		return errors.New(path + ": content differs from what rig wrote")
	}
	return nil
}
