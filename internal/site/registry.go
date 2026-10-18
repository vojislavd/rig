package site

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
)

// Errors callers can check for with errors.Is.
var (
	ErrExists   = errors.New("site already exists")
	ErrNotFound = errors.New("site not found")
)

// Site is one registered site. More fields (PHP version, workers,
// services) are added in later milestones.
type Site struct {
	Name       string `json:"name"`
	Dir        string `json:"dir"`                   // resolved absolute path, from SiteDir
	ReverbPort int    `json:"reverb_port,omitempty"` // 0 until Reverb is first started
	S3         bool   `json:"s3,omitempty"`          // has a Garage bucket (rig s3 enable)
}

// FilesHost is the name a site's S3 storage is reached at, covered by the
// site's own wildcard certificate: "foo" -> "files.foo.localhost".
func FilesHost(name string) string {
	return "files." + Host(name)
}

// Bucket is the Garage bucket (and key name) of a site: "rig-foo".
func Bucket(name string) string {
	return "rig-" + name
}

// Reverb ports are handed out from this range, one per site, on
// 127.0.0.1 only. They stay with the site so its .env keeps working.
const (
	MinReverbPort = 6001
	MaxReverbPort = 6999
)

// Registry is the set of all sites, keyed by name.
type Registry struct {
	sites map[string]Site
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{sites: make(map[string]Site)}
}

// Add registers s. It refuses invalid names, duplicates, and a folder
// whose name doesn't match the site name (folder = site name, §12).
func (r *Registry) Add(s Site) error {
	if err := ValidateName(s.Name); err != nil {
		return err
	}
	if !filepath.IsAbs(s.Dir) || filepath.Clean(s.Dir) != s.Dir {
		return fmt.Errorf("site %q: dir %q must be a clean absolute path", s.Name, s.Dir)
	}
	if filepath.Base(s.Dir) != s.Name {
		return fmt.Errorf("site %q: folder name %q must match the site name", s.Name, filepath.Base(s.Dir))
	}
	if s.ReverbPort != 0 && (s.ReverbPort < MinReverbPort || s.ReverbPort > MaxReverbPort) {
		return fmt.Errorf("site %q: reverb port %d outside %d-%d", s.Name, s.ReverbPort, MinReverbPort, MaxReverbPort)
	}
	if _, ok := r.sites[s.Name]; ok {
		return fmt.Errorf("%w: %q", ErrExists, s.Name)
	}
	if s.ReverbPort != 0 && r.portUsed(s.ReverbPort) {
		return fmt.Errorf("site %q: reverb port %d already used by another site", s.Name, s.ReverbPort)
	}
	r.sites[s.Name] = s
	return nil
}

func (r *Registry) portUsed(port int) bool {
	for _, s := range r.sites {
		if s.ReverbPort == port {
			return true
		}
	}
	return false
}

// ReverbPort returns the site's Reverb port, giving it the lowest free
// one first if it has none. changed reports whether a port was assigned.
func (r *Registry) ReverbPort(name string) (port int, changed bool, err error) {
	s, ok := r.sites[name]
	if !ok {
		return 0, false, fmt.Errorf("%w: %q", ErrNotFound, name)
	}
	if s.ReverbPort != 0 {
		return s.ReverbPort, false, nil
	}
	for p := MinReverbPort; p <= MaxReverbPort; p++ {
		if !r.portUsed(p) {
			s.ReverbPort = p
			r.sites[name] = s
			return p, true, nil
		}
	}
	return 0, false, errors.New("no free Reverb port left")
}

// Remove unregisters the named site.
func (r *Registry) Remove(name string) error {
	if _, ok := r.sites[name]; !ok {
		return fmt.Errorf("%w: %q", ErrNotFound, name)
	}
	delete(r.sites, name)
	return nil
}

// Get returns the named site and whether it exists.
func (r *Registry) Get(name string) (Site, bool) {
	s, ok := r.sites[name]
	return s, ok
}

// List returns all sites sorted by name. Go maps have no fixed order,
// so sorting keeps output stable.
func (r *Registry) List() []Site {
	out := make([]Site, 0, len(r.sites))
	for _, s := range r.sites {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// EnableS3 marks the site as having a Garage bucket.
func (r *Registry) EnableS3(name string) error {
	s, ok := r.sites[name]
	if !ok {
		return fmt.Errorf("%w: %q", ErrNotFound, name)
	}
	s.S3 = true
	r.sites[name] = s
	return nil
}

// ClearReverbPort releases the site's Reverb port.
func (r *Registry) ClearReverbPort(name string) error {
	s, ok := r.sites[name]
	if !ok {
		return fmt.Errorf("%w: %q", ErrNotFound, name)
	}
	s.ReverbPort = 0
	r.sites[name] = s
	return nil
}
