// Package stack ties rig's parts together: it keeps the nginx and PHP
// user services in place and links and unlinks sites.
package stack

import (
	"path/filepath"

	"github.com/vojislavd/rig/internal/nginx"
	"github.com/vojislavd/rig/internal/paths"
	"github.com/vojislavd/rig/internal/php"
)

// Layout is every folder rig uses.
type Layout struct {
	Code    string // ~/code
	Config  string // ~/.config/rig
	Logs    string // ~/.local/state/rig/logs
	Runtime string // /run/user/UID/rig
	Certs   string // ~/.local/share/rig/certs
	CA      string // ~/.local/share/rig/ca
	Units   string // ~/.config/systemd/user
}

// DefaultLayout resolves the standard folders for the current user.
func DefaultLayout() (Layout, error) {
	var l Layout
	var err error
	for _, f := range []struct {
		dst *string
		fn  func() (string, error)
	}{
		{&l.Code, paths.CodeDir},
		{&l.Config, paths.ConfigDir},
		{&l.Runtime, paths.RuntimeDir},
		{&l.Certs, paths.CertsDir},
		{&l.CA, paths.CADir},
		{&l.Units, paths.UnitsDir},
	} {
		if *f.dst, err = f.fn(); err != nil {
			return Layout{}, err
		}
	}
	state, err := paths.StateDir()
	if err != nil {
		return Layout{}, err
	}
	l.Logs = filepath.Join(state, "logs")
	return l, nil
}

func (l Layout) nginxDirs() nginx.Dirs {
	return nginx.Dirs{Conf: filepath.Join(l.Config, "nginx"), Logs: l.Logs, Runtime: filepath.Join(l.Runtime, "nginx")}
}

func (l Layout) phpDirs() php.Dirs {
	return php.Dirs{Conf: filepath.Join(l.Config, "php"), Logs: l.Logs, Runtime: l.Runtime, Code: l.Code}
}

// Registry is the path of sites.json.
func (l Layout) Registry() string {
	return filepath.Join(l.Config, "sites.json")
}
