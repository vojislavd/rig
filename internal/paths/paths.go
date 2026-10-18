// Package paths says where rig keeps its files, following
// the XDG base directory rules.
package paths

import (
	"errors"
	"os"
	"path/filepath"
)

// DataDir is rig's data folder: $XDG_DATA_HOME/rig, or ~/.local/share/rig.
func DataDir() (string, error) {
	return xdg("XDG_DATA_HOME", ".local/share")
}

// ConfigDir is rig's config folder: $XDG_CONFIG_HOME/rig, or ~/.config/rig.
func ConfigDir() (string, error) {
	return xdg("XDG_CONFIG_HOME", ".config")
}

// StateDir is rig's state folder (logs): $XDG_STATE_HOME/rig, or
// ~/.local/state/rig.
func StateDir() (string, error) {
	return xdg("XDG_STATE_HOME", ".local/state")
}

// RuntimeDir is rig's folder for sockets, pid and temp files:
// $XDG_RUNTIME_DIR/rig. systemd creates $XDG_RUNTIME_DIR (/run/user/UID)
// at login, private to the user (0700) and emptied at logout. There is no
// safe fallback, so it must be set.
func RuntimeDir() (string, error) {
	v := os.Getenv("XDG_RUNTIME_DIR")
	if v == "" || !filepath.IsAbs(v) {
		return "", errors.New("XDG_RUNTIME_DIR is not set to an absolute path (are you logged in through systemd?)")
	}
	return filepath.Join(filepath.Clean(v), "rig"), nil
}

// CodeDir is where site folders live: ~/code. It will
// become configurable in config.json.
func CodeDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(home) {
		return "", errors.New("home folder is not an absolute path")
	}
	return filepath.Join(home, "code"), nil
}

// UnitsDir is where systemd looks for the user's own units.
func UnitsDir() (string, error) {
	return xdgPlain("XDG_CONFIG_HOME", ".config", "systemd/user")
}

// CADir holds the CA certificate and key.
func CADir() (string, error) {
	return sub(DataDir, "ca")
}

// BackupsDir holds database dumps.
func BackupsDir() (string, error) {
	return sub(DataDir, "backups")
}

// CertsDir holds the site certificates.
func CertsDir() (string, error) {
	return sub(DataDir, "certs")
}

// xdg returns $env/rig if env is set to an absolute path, otherwise
// ~/fallback/rig. The XDG spec says relative values must be ignored; it
// also keeps rig from writing secrets relative to the current folder.
func xdg(env, fallback string) (string, error) {
	if v := os.Getenv(env); v != "" && filepath.IsAbs(v) {
		return filepath.Join(filepath.Clean(v), "rig"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(home) {
		return "", errors.New("home folder is not an absolute path")
	}
	return filepath.Join(home, fallback, "rig"), nil
}

// xdgPlain is like xdg but with a custom last part instead of "rig".
func xdgPlain(env, fallback, last string) (string, error) {
	if v := os.Getenv(env); v != "" && filepath.IsAbs(v) {
		return filepath.Join(filepath.Clean(v), last), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(home) {
		return "", errors.New("home folder is not an absolute path")
	}
	return filepath.Join(home, fallback, last), nil
}

func sub(base func() (string, error), name string) (string, error) {
	dir, err := base()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, name), nil
}
