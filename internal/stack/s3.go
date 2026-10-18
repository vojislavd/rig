package stack

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/vojislavd/rig/internal/fsutil"
	"github.com/vojislavd/rig/internal/garage"
	"github.com/vojislavd/rig/internal/nginx"
	"github.com/vojislavd/rig/internal/site"
)

func (st *Stack) garageConf() string { return filepath.Join(st.L.Config, "garage.toml") }
func (st *Stack) garageData() string { return filepath.Join(filepath.Dir(st.L.Certs), "garage") }

// GarageAdmin returns a client for the running Garage's admin API.
// Tests replace it.
var newGarageAdmin = func(token string) garageAdmin { return garage.NewAdmin(garage.AdminAddr, token) }

// garageAdmin is the part of garage.Admin the stack uses.
type garageAdmin interface {
	WaitReady(time.Duration) error
	InitLayout() error
	Bucket(alias string) (string, error)
	Key(name string) (id, secret string, err error)
	Allow(bucketID, keyID string) error
}

// StartS3 starts Garage now (on demand, like Mailpit). The first time it
// creates garage.toml with fresh secrets (0600) and gives the single
// storage node its role. While Garage is stopped, uploads fail.
func (st *Stack) StartS3() error {
	for _, d := range garage.DataDirs(st.garageData()) {
		if err := fsutil.EnsurePrivateDir(d); err != nil {
			return err
		}
	}
	if err := fsutil.EnsurePrivateDir(st.L.Config); err != nil {
		return err
	}
	sec, err := st.garageSecrets()
	if err != nil {
		return err
	}
	conf, err := garage.Config(st.garageData(), sec)
	if err != nil {
		return err
	}
	if _, err := writeIfChanged(file{path: st.garageConf(), content: conf, mode: 0o600}); err != nil {
		return err
	}
	unit, err := garage.Unit(st.garageConf(), st.garageData())
	if err != nil {
		return err
	}
	if err := os.MkdirAll(st.L.Units, 0o755); err != nil {
		return err
	}
	changed, err := writeIfChanged(file{path: filepath.Join(st.L.Units, garage.UnitName), content: unit, mode: 0o644})
	if err != nil {
		return err
	}
	if changed {
		if err := st.S.DaemonReload(); err != nil {
			return err
		}
	}
	if err := st.S.Start(garage.UnitName); err != nil {
		return err
	}
	admin := newGarageAdmin(sec.Admin)
	if err := admin.WaitReady(15 * time.Second); err != nil {
		return err
	}
	return admin.InitLayout()
}

// garageSecrets loads Garage's secrets, or makes new ones the first time.
// An existing config must be private.
func (st *Stack) garageSecrets() (garage.Secrets, error) {
	if _, err := os.Lstat(st.garageConf()); errors.Is(err, fs.ErrNotExist) {
		return garage.NewSecrets()
	}
	if err := fsutil.CheckPrivate(st.garageConf(), false); err != nil {
		return garage.Secrets{}, err
	}
	return garage.LoadSecrets(st.garageConf())
}

// StopS3 stops Garage. Stored files and garage.toml are kept.
func (st *Stack) StopS3() error {
	path := filepath.Join(st.L.Units, garage.UnitName)
	if !fileExists(path) {
		return nil
	}
	if err := st.S.Stop(garage.UnitName); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return st.S.DaemonReload()
}

// S3State returns systemd's state word for Garage, or "stopped".
func (st *Stack) S3State() string {
	if !fileExists(filepath.Join(st.L.Units, garage.UnitName)) {
		return "stopped"
	}
	return st.S.IsActive(garage.UnitName)
}

// EnableS3 gives the site a Garage bucket (rig-<site>) and a key allowed
// on that bucket only, and serves https://files.<site>.localhost. Garage
// must be running. Safe to run again: the bucket and key are reused.
func (st *Stack) EnableS3(name string) error {
	admin, err := st.runningGarage()
	if err != nil {
		return err
	}
	return site.Update(st.L.Registry(), func(r *site.Registry) error {
		if _, ok := r.Get(name); !ok {
			return fmt.Errorf("%w: %q", site.ErrNotFound, name)
		}
		bucket, err := admin.Bucket(site.Bucket(name))
		if err != nil {
			return err
		}
		keyID, _, err := admin.Key(site.Bucket(name))
		if err != nil {
			return err
		}
		if err := admin.Allow(bucket, keyID); err != nil {
			return err
		}
		if err := r.EnableS3(name); err != nil {
			return err
		}
		s, _ := r.Get(name)
		conf, err := st.siteConfig(s)
		if err != nil {
			return err
		}
		changed, err := writeIfChanged(file{path: nginx.SiteConfigPath(st.L.nginxDirs().Conf, name), content: conf, mode: 0o600})
		if err != nil || !changed {
			return err
		}
		if err := st.S.Reload(nginx.UnitName); err != nil {
			return err
		}
		return st.waitServed(site.FilesHost(name), true)
	})
}

// s3Credentials returns the site's access key ID and secret from Garage.
func (st *Stack) s3Credentials(name string) (id, secret string, err error) {
	admin, err := st.runningGarage()
	if err != nil {
		return "", "", err
	}
	return admin.Key(site.Bucket(name))
}

// runningGarage returns an admin client if Garage runs, or a clear error.
func (st *Stack) runningGarage() (garageAdmin, error) {
	if st.S3State() != "active" {
		return nil, errors.New("Garage is not running; start it with rig s3 start")
	}
	sec, err := st.garageSecrets()
	if err != nil {
		return nil, err
	}
	admin := newGarageAdmin(sec.Admin)
	if err := admin.WaitReady(5 * time.Second); err != nil {
		return nil, err
	}
	return admin, nil
}
