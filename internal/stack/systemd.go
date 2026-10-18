package stack

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

// Systemd is the part of "systemctl --user" rig uses. Tests replace it.
type Systemd interface {
	DaemonReload() error
	EnableNow(units ...string) error
	Start(unit string) error
	Stop(units ...string) error
	Reload(unit string) error
	Restart(unit string) error
	// IsActive returns systemd's state word: "active", "failed", ...
	IsActive(unit string) string
}

// UserSystemd runs /usr/bin/systemctl --user. No sudo: these are the
// user's own services.
type UserSystemd struct{}

func (UserSystemd) DaemonReload() error { return systemctl("daemon-reload") }

func (UserSystemd) EnableNow(units ...string) error {
	return systemctl(append([]string{"enable", "--now"}, units...)...)
}

func (UserSystemd) Start(unit string) error { return systemctl("start", unit) }

func (UserSystemd) Stop(units ...string) error {
	return systemctl(append([]string{"stop"}, units...)...)
}

func (UserSystemd) Restart(unit string) error { return systemctl("restart", unit) }

func (UserSystemd) IsActive(unit string) string {
	out, _ := exec.Command("/usr/bin/systemctl", "--user", "is-active", unit).Output()
	return strings.TrimSpace(string(out))
}

// Reload asks the unit to reload. For nginx, the unit's ExecReload checks
// the config with "nginx -t" first and refuses a broken one.
func (UserSystemd) Reload(unit string) error { return systemctl("reload", unit) }

func systemctl(args ...string) error {
	cmd := exec.Command("/usr/bin/systemctl", append([]string{"--user"}, args...)...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("systemctl --user %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(out.String()))
	}
	return nil
}
