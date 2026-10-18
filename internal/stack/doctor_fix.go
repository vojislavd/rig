package stack

import (
	"time"

	"github.com/vojislavd/rig/internal/ca"
	"github.com/vojislavd/rig/internal/fsutil"
	"github.com/vojislavd/rig/internal/nginx"
	"github.com/vojislavd/rig/internal/site"
)

// Repair is "rig doctor --fix": it puts back what rig owns and returns
// one line per change.
//   - service files (nginx.conf, PHP pool, both units) and every site's
//     nginx config are rewritten if they differ from rig's version
//   - site certificates are renewed if they are missing, invalid or
//     expire within 30 days
//
// Security: it only rewrites files rig generates, from sites.json, the
// same way link does. Leftovers it doesn't know (an extra config in
// rig.d, a stray unit) are only reported by Doctor, never deleted: rig
// can't tell what they are. It never needs sudo; a missing install step
// stays "rig install".
func (st *Stack) Repair() ([]string, error) {
	var changes []string
	unlock, err := st.lockRegistry()
	if err != nil {
		return nil, err
	}
	defer unlock()

	before := map[string]string{}
	files, err := st.serviceFiles()
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		before[f.path] = fileDetail(f)
	}
	confChanged, err := st.ensureServices()
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		if before[f.path] != "" {
			changes = append(changes, "rewrote "+st.short(f.path))
		}
	}

	r, err := site.Load(st.L.Registry())
	if err != nil {
		return nil, err
	}
	authority, err := ca.Load(st.L.CA)
	if err != nil {
		return nil, err
	}
	if err := fsutil.EnsurePrivateDir(st.L.Certs); err != nil {
		return nil, err
	}
	for _, s := range r.List() {
		status, err := authority.LeafStatus(st.L.Certs, s.Name, st.Now())
		if err != nil {
			return changes, err
		}
		if status.Renew {
			leaf, err := authority.IssueLeaf(s.Name, st.Now())
			if err != nil {
				return changes, err
			}
			if err := leaf.Save(st.L.Certs, s.Name); err != nil {
				return changes, err
			}
			changes = append(changes, "renewed certificate for "+s.Name+" ("+status.Reason+")")
			confChanged = true
		}
		conf, err := st.siteConfig(s)
		if err != nil {
			return changes, err
		}
		f := file{nginx.SiteConfigPath(st.L.nginxDirs().Conf, s.Name), conf, 0o600}
		changed, err := writeIfChanged(f)
		if err != nil {
			return changes, err
		}
		if changed {
			changes = append(changes, "rewrote "+st.short(f.path))
			confChanged = true
		}
	}

	// nginx reads certificates and configs only when it (re)loads.
	if confChanged {
		if err := st.S.Reload(nginx.UnitName); err != nil {
			return changes, err
		}
		for _, s := range r.List() {
			if err := st.waitServed(site.Host(s.Name), true); err != nil {
				return changes, err
			}
		}
		changes = append(changes, "reloaded nginx")
	}
	return changes, nil
}

// lockRegistry holds the sites.json lock, so no link or unlink runs
// while Repair rewrites files from it.
func (st *Stack) lockRegistry() (func() error, error) {
	if err := fsutil.EnsurePrivateDir(st.L.Config); err != nil {
		return nil, err
	}
	return fsutil.Lock(st.L.Registry()+".lock", 5*time.Second)
}
