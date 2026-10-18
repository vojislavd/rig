package stack

import (
	"bytes"
	"crypto/tls"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/vojislavd/rig/internal/ca"
	"github.com/vojislavd/rig/internal/fsutil"
	"github.com/vojislavd/rig/internal/garage"
	"github.com/vojislavd/rig/internal/nginx"
	"github.com/vojislavd/rig/internal/php"
	"github.com/vojislavd/rig/internal/site"
)

// Stack links sites and keeps the services they need running.
type Stack struct {
	L   Layout
	S   Systemd
	Now func() time.Time
	// Served reports whether nginx currently completes a TLS handshake
	// for host. nil skips the check (tests without a running nginx).
	Served func(host string) bool
}

// New returns a Stack for the current user with real systemd.
func New() (*Stack, error) {
	l, err := DefaultLayout()
	if err != nil {
		return nil, err
	}
	return &Stack{L: l, S: UserSystemd{}, Now: time.Now, Served: tlsServed}, nil
}

// file is one generated file rig keeps in place.
type file struct {
	path    string
	content []byte
	mode    fs.FileMode
}

// serviceFiles returns nginx.conf, the PHP pool and both user units.
func (st *Stack) serviceFiles() ([]file, error) {
	nd, pd := st.L.nginxDirs(), st.L.phpDirs()
	mainConf, err := nginx.MainConfig(nd)
	if err != nil {
		return nil, err
	}
	nginxUnit, err := nginx.Unit(nginx.UnitDirs{Dirs: nd, Code: st.L.Code, Certs: st.L.Certs})
	if err != nil {
		return nil, err
	}
	pool, err := php.PoolConfig(pd, php.Default)
	if err != nil {
		return nil, err
	}
	phpUnit, err := php.Unit(pd, php.Default)
	if err != nil {
		return nil, err
	}
	return []file{
		{filepath.Join(nd.Conf, "nginx.conf"), mainConf, 0o600},
		{php.ConfPath(pd, php.Default), pool, 0o600},
		{filepath.Join(st.L.Units, nginx.UnitName), nginxUnit, 0o644},
		{filepath.Join(st.L.Units, php.Default.UnitName()), phpUnit, 0o644},
	}, nil
}

// ensureServices writes any service file whose content changed, reloads
// systemd if a unit changed, and makes sure nginx and PHP run. Safe to
// call any number of times. confChanged reports whether nginx.conf or the
// PHP pool changed (a running nginx then needs a reload).
func (st *Stack) ensureServices() (confChanged bool, err error) {
	for _, dir := range []string{
		filepath.Join(st.L.Config, "nginx", "rig.d"),
		filepath.Join(st.L.Config, "php"),
		st.L.Certs,
	} {
		if err := fsutil.EnsurePrivateDir(dir); err != nil {
			return false, err
		}
	}
	// ~/.config/systemd/user is a normal (not private) folder.
	if err := os.MkdirAll(st.L.Units, 0o755); err != nil {
		return false, err
	}

	files, err := st.serviceFiles()
	if err != nil {
		return false, err
	}
	unitsChanged := false
	for _, f := range files {
		changed, err := writeIfChanged(f)
		if err != nil {
			return false, err
		}
		switch {
		case changed && filepath.Dir(f.path) == st.L.Units:
			unitsChanged = true
		case changed:
			confChanged = true
		}
	}
	if unitsChanged {
		if err := st.S.DaemonReload(); err != nil {
			return false, err
		}
	}
	return confChanged, st.S.EnableNow(nginx.UnitName, php.Default.UnitName())
}

// writeIfChanged writes f atomically unless the file already has exactly
// this content and mode.
func writeIfChanged(f file) (bool, error) {
	if info, err := os.Lstat(f.path); err == nil && info.Mode().IsRegular() && info.Mode().Perm() == f.mode {
		if old, err := os.ReadFile(f.path); err == nil && bytes.Equal(old, f.content) {
			return false, nil
		}
	}
	return true, fsutil.WriteFileAtomic(f.path, f.content, f.mode)
}

// Link registers the project folder dir as a site and serves it at
// https://<name>.localhost. If name is empty, the folder name is used;
// otherwise it must equal the folder name (folder = site name).
//
// Everything happens while sites.json is locked. If a step fails, the
// files created so far are removed and the site is not registered.
func (st *Stack) Link(dir, name string) (site.Site, error) {
	realDir, err := site.SiteDir(st.L.Code, dir)
	if err != nil {
		return site.Site{}, err
	}
	if name == "" {
		name = filepath.Base(realDir)
	}
	if err := site.ValidateName(name); err != nil {
		return site.Site{}, err
	}
	if filepath.Base(realDir) != name {
		return site.Site{}, fmt.Errorf("folder %q must be named %q (folder name = site name)", realDir, name)
	}
	if info, err := os.Stat(filepath.Join(realDir, "public")); err != nil || !info.IsDir() {
		return site.Site{}, fmt.Errorf("%s has no public/ folder; is it a Laravel project?", realDir)
	}
	authority, err := ca.Load(st.L.CA)
	if errors.Is(err, fs.ErrNotExist) {
		return site.Site{}, errors.New("no CA yet; run rig install first")
	}
	if err != nil {
		return site.Site{}, err
	}

	// sites.json and its lock live here; create it (0700) on first use.
	if err := fsutil.EnsurePrivateDir(st.L.Config); err != nil {
		return site.Site{}, err
	}
	s := site.Site{Name: name, Dir: realDir}
	err = site.Update(st.L.Registry(), func(r *site.Registry) error {
		if err := r.Add(s); err != nil {
			return err
		}
		return st.linkFiles(authority, s)
	})
	return s, err
}

// linkFiles issues the certificate, writes the site config and reloads
// nginx, undoing its own changes on failure.
func (st *Stack) linkFiles(authority *ca.CA, s site.Site) (err error) {
	var undo []func()
	defer func() {
		if err != nil {
			for i := len(undo) - 1; i >= 0; i-- {
				undo[i]()
			}
		}
	}()

	status, err := authority.LeafStatus(st.L.Certs, s.Name, st.Now())
	if err != nil {
		return err
	}
	if status.Renew {
		hadCert := fileExists(filepath.Join(st.L.Certs, s.Name+".crt"))
		leaf, err := authority.IssueLeaf(s.Name, st.Now())
		if err != nil {
			return err
		}
		// ensureServices creates the certs folder; do it before saving.
		if err := fsutil.EnsurePrivateDir(st.L.Certs); err != nil {
			return err
		}
		if err := leaf.Save(st.L.Certs, s.Name); err != nil {
			return err
		}
		if !hadCert {
			undo = append(undo, func() { st.removeCert(s.Name) })
		}
	}

	conf, err := st.siteConfig(s)
	if err != nil {
		return err
	}
	if err := fsutil.EnsurePrivateDir(filepath.Join(st.L.Config, "nginx", "rig.d")); err != nil {
		return err
	}
	confPath := nginx.SiteConfigPath(st.L.nginxDirs().Conf, s.Name)
	if err := fsutil.WriteFileAtomic(confPath, conf, 0o600); err != nil {
		return err
	}
	undo = append(undo, func() {
		os.Remove(confPath)
		st.S.Reload(nginx.UnitName)
	})

	if _, err := st.ensureServices(); err != nil {
		return err
	}
	if err := st.S.Reload(nginx.UnitName); err != nil {
		return err
	}
	return st.waitServed(site.Host(s.Name), true)
}

// Unlink stops serving the named site: its nginx config and certificate
// are removed and nginx reloads. The project folder is never touched.
func (st *Stack) Unlink(name string) error {
	if err := site.ValidateName(name); err != nil {
		return err
	}
	if err := fsutil.EnsurePrivateDir(st.L.Config); err != nil {
		return err
	}
	return site.Update(st.L.Registry(), func(r *site.Registry) error {
		if _, ok := r.Get(name); !ok {
			return fmt.Errorf("%w: %q", site.ErrNotFound, name)
		}
		if err := st.StopWorkers(name, ""); err != nil {
			return err
		}
		if err := r.Remove(name); err != nil {
			return err
		}
		confPath := nginx.SiteConfigPath(st.L.nginxDirs().Conf, name)
		if err := os.Remove(confPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		if err := st.S.Reload(nginx.UnitName); err != nil {
			return err
		}
		if err := st.waitServed(site.Host(name), false); err != nil {
			return err
		}
		st.removeCert(name)
		return nil
	})
}

func (st *Stack) removeCert(name string) {
	os.Remove(filepath.Join(st.L.Certs, name+".crt"))
	os.Remove(filepath.Join(st.L.Certs, name+".key"))
}

// SiteInfo is one row of "rig sites".
type SiteInfo struct {
	Name       string    `json:"name"`
	URL        string    `json:"url"`
	Dir        string    `json:"dir"`
	CertExpiry time.Time `json:"cert_expiry"`
	CertIssue  string    `json:"cert_issue,omitempty"` // why the cert needs renewing, if it does
}

// Sites lists the registered sites with their certificate state.
func (st *Stack) Sites() ([]SiteInfo, error) {
	r, err := site.Load(st.L.Registry())
	if err != nil {
		return nil, err
	}
	authority, caErr := ca.Load(st.L.CA)
	var out []SiteInfo
	for _, s := range r.List() {
		info := SiteInfo{Name: s.Name, URL: "https://" + site.Host(s.Name), Dir: s.Dir}
		if caErr != nil {
			info.CertIssue = "CA unavailable: " + caErr.Error()
		} else if status, err := authority.LeafStatus(st.L.Certs, s.Name, st.Now()); err != nil {
			info.CertIssue = err.Error()
		} else {
			info.CertExpiry = status.NotAfter
			if status.Renew {
				info.CertIssue = status.Reason
			}
		}
		out = append(out, info)
	}
	return out, nil
}

func fileExists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// waitServed waits until nginx serves host (want=true) or refuses it
// (want=false). "systemctl reload" returns before nginx has switched:
// old workers keep the old config for a moment, so rig checks the real
// result instead of trusting the reload.
func (st *Stack) waitServed(host string, want bool) error {
	if st.Served == nil {
		return nil
	}
	for i := 0; i < 30; i++ {
		if st.Served(host) == want {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	if want {
		return fmt.Errorf("nginx does not serve %s after reloading", host)
	}
	return fmt.Errorf("nginx still serves %s after reloading", host)
}

// tlsServed reports whether nginx on 127.0.0.1:8443 completes a TLS
// handshake for host. Unknown names are refused (ssl_reject_handshake).
//
// Security: InsecureSkipVerify is fine here because nothing is sent or
// trusted over this connection; it only checks whether nginx presents a
// certificate for the name, and is closed right away.
func tlsServed(host string) bool {
	d := &net.Dialer{Timeout: time.Second}
	addr := net.JoinHostPort(nginx.ListenAddr, strconv.Itoa(nginx.HTTPSPort))
	conn, err := tls.DialWithDialer(d, "tcp", addr, &tls.Config{ServerName: host, InsecureSkipVerify: true})
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// siteConfig renders the nginx config for a registered site.
func (st *Stack) siteConfig(s site.Site) ([]byte, error) {
	return nginx.SiteConfig(nginx.Site{
		Name:       s.Name,
		Dir:        s.Dir,
		Certs:      st.L.Certs,
		Socket:     php.Socket(st.L.phpDirs(), php.Default),
		ReverbPort: s.ReverbPort,
		S3:         s.S3,
		S3Addr:     garage.S3Addr,
	})
}
