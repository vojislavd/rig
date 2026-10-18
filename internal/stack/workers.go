package stack

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/vojislavd/rig/internal/nginx"
	"github.com/vojislavd/rig/internal/php"
	"github.com/vojislavd/rig/internal/site"
	"github.com/vojislavd/rig/internal/worker"
)

// StartWorker starts a worker for the site now. Workers are on demand:
// nothing records them, and they never start by themselves (no login
// start; logging out stops them).
func (st *Stack) StartWorker(name, kind string) error {
	if err := worker.CheckKind(kind); err != nil {
		return err
	}
	r, err := site.Load(st.L.Registry())
	if err != nil {
		return err
	}
	s, ok := r.Get(name)
	if !ok {
		return fmt.Errorf("%w: %q", site.ErrNotFound, name)
	}
	port := 0
	if kind == "reverb" {
		// Check first, before a port or nginx route is set up for nothing.
		if !hasPackage(s.Dir, "laravel/reverb") {
			return fmt.Errorf("%s doesn't use Laravel Reverb (laravel/reverb is not in composer.json)", name)
		}
		if port, err = st.ensureReverbRoute(name); err != nil {
			return err
		}
	}
	unit, err := worker.Unit(name, s.Dir, kind, php.Default.CLI, port)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(st.L.Units, 0o755); err != nil {
		return err
	}
	unitName := worker.UnitName(name, kind)
	changed, err := writeIfChanged(file{path: filepath.Join(st.L.Units, unitName), content: unit, mode: 0o644})
	if err != nil {
		return err
	}
	if changed {
		if err := st.S.DaemonReload(); err != nil {
			return err
		}
	}
	return st.S.Start(unitName)
}

// StopWorkers stops the site's worker of the given kind, or all of the
// site's workers if kind is "". Their unit files are removed.
func (st *Stack) StopWorkers(name, kind string) error {
	if err := site.ValidateName(name); err != nil {
		return err
	}
	if kind != "" {
		if err := worker.CheckKind(kind); err != nil {
			return err
		}
	}
	var units []string
	for _, k := range worker.KindNames() {
		if kind != "" && k != kind {
			continue
		}
		u := worker.UnitName(name, k)
		if fileExists(filepath.Join(st.L.Units, u)) {
			units = append(units, u)
		}
	}
	if len(units) == 0 {
		if kind != "" {
			return fmt.Errorf("no %s worker running for %s", kind, name)
		}
		return nil
	}
	if err := st.S.Stop(units...); err != nil {
		return err
	}
	for _, u := range units {
		if err := os.Remove(filepath.Join(st.L.Units, u)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return st.S.DaemonReload()
}

// StopAllWorkers stops every worker of every site.
func (st *Stack) StopAllWorkers() error {
	list, err := st.Workers()
	if err != nil {
		return err
	}
	stopped := map[string]bool{}
	for _, w := range list {
		if !stopped[w.Site] {
			if err := st.StopWorkers(w.Site, ""); err != nil {
				return err
			}
			stopped[w.Site] = true
		}
	}
	return nil
}

// RestartWorker restarts a running worker, e.g. after code changes:
// queue workers keep the old code in memory until restarted.
func (st *Stack) RestartWorker(name, kind string) error {
	if err := worker.CheckKind(kind); err != nil {
		return err
	}
	if err := site.ValidateName(name); err != nil {
		return err
	}
	u := worker.UnitName(name, kind)
	if !fileExists(filepath.Join(st.L.Units, u)) {
		return fmt.Errorf("no %s worker running for %s", kind, name)
	}
	return st.S.Restart(u)
}

// WorkerInfo is one row of "rig worker list".
type WorkerInfo struct {
	Site  string `json:"site"`
	Kind  string `json:"kind"`
	Unit  string `json:"unit"`
	State string `json:"state"` // systemd's word: active, failed, ...
}

// Workers lists the workers that were started (their unit files exist),
// for registered sites, with their current state.
func (st *Stack) Workers() ([]WorkerInfo, error) {
	r, err := site.Load(st.L.Registry())
	if err != nil {
		return nil, err
	}
	var out []WorkerInfo
	for _, s := range r.List() {
		for _, k := range worker.KindNames() {
			u := worker.UnitName(s.Name, k)
			if fileExists(filepath.Join(st.L.Units, u)) {
				out = append(out, WorkerInfo{Site: s.Name, Kind: k, Unit: u, State: st.S.IsActive(u)})
			}
		}
	}
	return out, nil
}

// ensureReverbRoute gives the site a Reverb port (the first time) and
// makes sure its nginx config routes /app and /apps to it. The port stays
// with the site in sites.json, so its .env keeps working.
func (st *Stack) ensureReverbRoute(name string) (port int, err error) {
	err = site.Update(st.L.Registry(), func(r *site.Registry) error {
		p, _, err := r.ReverbPort(name)
		if err != nil {
			return err
		}
		port = p
		// The Reverb route needs the websocket map in nginx.conf, so bring
		// the main config up to date first.
		mainChanged, err := st.ensureServices()
		if err != nil {
			return err
		}
		s, _ := r.Get(name)
		conf, err := st.siteConfig(s)
		if err != nil {
			return err
		}
		siteChanged, err := writeIfChanged(file{path: nginx.SiteConfigPath(st.L.nginxDirs().Conf, name), content: conf, mode: 0o600})
		if err != nil {
			return err
		}
		if !mainChanged && !siteChanged {
			return nil
		}
		if err := st.S.Reload(nginx.UnitName); err != nil {
			return err
		}
		return st.waitServed(site.Host(name), true)
	})
	return port, err
}

// RemoveReverb stops the site's Reverb worker (if running), releases its
// Reverb port and removes the nginx route for /app and /apps.
func (st *Stack) RemoveReverb(name string) error {
	if err := site.ValidateName(name); err != nil {
		return err
	}
	if err := st.StopWorkers(name, "reverb"); err != nil && !strings.Contains(err.Error(), "no reverb worker running") {
		return err
	}
	return site.Update(st.L.Registry(), func(r *site.Registry) error {
		s, ok := r.Get(name)
		if !ok {
			return fmt.Errorf("%w: %q", site.ErrNotFound, name)
		}
		if s.ReverbPort == 0 {
			return nil
		}
		if err := r.ClearReverbPort(name); err != nil {
			return err
		}
		s, _ = r.Get(name)
		conf, err := st.siteConfig(s)
		if err != nil {
			return err
		}
		if _, err := writeIfChanged(file{path: nginx.SiteConfigPath(st.L.nginxDirs().Conf, name), content: conf, mode: 0o600}); err != nil {
			return err
		}
		return st.S.Reload(nginx.UnitName)
	})
}

// hasPackage reports whether the project's composer.json requires pkg.
func hasPackage(dir, pkg string) bool {
	data, err := os.ReadFile(filepath.Join(dir, "composer.json"))
	if err != nil {
		return false
	}
	var c struct {
		Require    map[string]string `json:"require"`
		RequireDev map[string]string `json:"require-dev"`
	}
	if json.Unmarshal(data, &c) != nil {
		return false
	}
	_, a := c.Require[pkg]
	_, b := c.RequireDev[pkg]
	return a || b
}
