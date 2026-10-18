package stack

import (
	"bytes"
	"encoding/pem"
	"os"
	"os/exec"
	"strings"

	"github.com/vojislavd/rig/internal/ca"
	"github.com/vojislavd/rig/internal/db"
	"github.com/vojislavd/rig/internal/garage"
	"github.com/vojislavd/rig/internal/install"
	"github.com/vojislavd/rig/internal/mailpit"
	"github.com/vojislavd/rig/internal/nginx"
	"github.com/vojislavd/rig/internal/php"
	"github.com/vojislavd/rig/internal/site"
)

// Report is everything "rig status" shows. Its JSON form is what the
// dashboard and other front-ends read.
type Report struct {
	Install  InstallStatus   `json:"install"`
	Services []ServiceStatus `json:"services"`
	Sites    []SiteStatus    `json:"sites"`
}

// InstallStatus covers what rig install set up.
type InstallStatus struct {
	PortRedirect string `json:"port_redirect"` // systemd state of rig-nat
	CATrusted    bool   `json:"ca_trusted"`    // trust store holds exactly this CA
}

// ServiceStatus is one of rig's services.
type ServiceStatus struct {
	Name     string `json:"name"`
	Unit     string `json:"unit"`
	State    string `json:"state"`     // active, inactive, failed, stopped, ...
	OnDemand bool   `json:"on_demand"` // started by hand (rig mail/s3 start)
}

// SiteStatus is one site with everything attached to it.
type SiteStatus struct {
	SiteInfo
	Database   string   `json:"database"` // "rig_x", "" if none, "?" if unknown
	S3         bool     `json:"s3"`
	FilesURL   string   `json:"files_url,omitempty"`   // S3 endpoint, if S3
	ReverbPort int      `json:"reverb_port,omitempty"` // 0 if Reverb was never started
	Workers    []string `json:"workers"`               // running workers
}

// systemState returns the state of a system (not user) unit. Reading it
// needs no privileges. A variable so tests can fake it.
var systemState = func(unit string) string {
	out, _ := exec.Command("/usr/bin/systemctl", "is-active", unit).Output()
	return strings.TrimSpace(string(out))
}

// trustAnchor is read through a variable so tests can point elsewhere.
var trustAnchor = install.TrustAnchorPath

// Status gathers the report. dbList lists rig databases; nil, or an
// error from it, shows databases as unknown rather than failing.
func (st *Stack) Status(dbList func() ([]string, error)) (Report, error) {
	var rep Report
	rep.Install.PortRedirect = systemState(install.NatUnitName)
	rep.Install.CATrusted = st.caTrusted()

	rep.Services = []ServiceStatus{
		{Name: "nginx", Unit: nginx.UnitName, State: st.S.IsActive(nginx.UnitName)},
		{Name: "php " + php.Default.Name, Unit: php.Default.UnitName(), State: st.S.IsActive(php.Default.UnitName())},
		{Name: "mail", Unit: mailpit.UnitName, State: st.MailState(), OnDemand: true},
		{Name: "s3", Unit: garage.UnitName, State: st.S3State(), OnDemand: true},
	}

	sites, err := st.Sites()
	if err != nil {
		return rep, err
	}
	r, err := site.Load(st.L.Registry())
	if err != nil {
		return rep, err
	}
	var dbs map[string]bool
	if dbList != nil {
		if names, err := dbList(); err == nil {
			dbs = map[string]bool{}
			for _, n := range names {
				dbs[n] = true
			}
		}
	}
	workers, err := st.Workers()
	if err != nil {
		return rep, err
	}
	for _, info := range sites {
		s, _ := r.Get(info.Name)
		ss := SiteStatus{SiteInfo: info, S3: s.S3, ReverbPort: s.ReverbPort, Workers: []string{}}
		if s.S3 {
			ss.FilesURL = "https://" + site.FilesHost(s.Name)
		}
		dbName, _ := db.Name(s.Name)
		switch {
		case dbs == nil:
			ss.Database = "?"
		case dbs[dbName]:
			ss.Database = dbName
		}
		for _, w := range workers {
			if w.Site == s.Name && w.State == "active" {
				ss.Workers = append(ss.Workers, w.Kind)
			}
		}
		rep.Sites = append(rep.Sites, ss)
	}
	return rep, nil
}

// caTrusted reports whether the system trust store holds exactly rig's
// current CA certificate (not merely some file at that path).
func (st *Stack) caTrusted() bool {
	authority, err := ca.Load(st.L.CA)
	if err != nil {
		return false
	}
	data, err := os.ReadFile(trustAnchor)
	if err != nil {
		return false
	}
	block, _ := pem.Decode(data)
	return block != nil && bytes.Equal(block.Bytes, authority.Cert.Raw)
}
