package stack

import (
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/vojislavd/rig/internal/ca"
)

func fakeSystem(t *testing.T, state string, anchor string) {
	t.Helper()
	oldState, oldAnchor := systemState, trustAnchor
	systemState = func(string) string { return state }
	trustAnchor = anchor
	t.Cleanup(func() { systemState, trustAnchor = oldState, oldAnchor })
}

func TestStatus(t *testing.T) {
	st, _ := linkedStack(t)
	anchor := filepath.Join(t.TempDir(), "rig-ca.crt")
	authority, err := ca.Load(st.L.CA)
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(anchor, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: authority.Cert.Raw}), 0o644)
	fakeSystem(t, "active", anchor)
	st.StartWorker("foo", "queue")

	rep, err := st.Status(func() ([]string, error) { return []string{"rig_foo"}, nil })
	if err != nil {
		t.Fatal(err)
	}
	if rep.Install.PortRedirect != "active" || !rep.Install.CATrusted {
		t.Errorf("install = %+v", rep.Install)
	}
	if len(rep.Services) != 4 || rep.Services[2].Name != "mail" || rep.Services[2].State != "stopped" || !rep.Services[2].OnDemand {
		t.Errorf("services = %+v", rep.Services)
	}
	if len(rep.Sites) != 1 {
		t.Fatalf("sites = %+v", rep.Sites)
	}
	s := rep.Sites[0]
	if s.Database != "rig_foo" || s.URL != "https://foo.localhost" || !slices.Equal(s.Workers, []string{"queue"}) || s.S3 {
		t.Errorf("site = %+v", s)
	}
}

func TestStatusUnknownDBAndUntrustedCA(t *testing.T) {
	st, _ := linkedStack(t)
	other, _ := ca.New(st.Now())
	anchor := filepath.Join(t.TempDir(), "rig-ca.crt")
	os.WriteFile(anchor, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: other.Cert.Raw}), 0o644)
	fakeSystem(t, "inactive", anchor)

	rep, err := st.Status(func() ([]string, error) { return nil, errors.New("no login") })
	if err != nil {
		t.Fatal(err)
	}
	if rep.Install.CATrusted {
		t.Error("a different CA in the trust store counted as trusted")
	}
	if rep.Sites[0].Database != "?" {
		t.Errorf("database = %q, want ? when MariaDB can't be asked", rep.Sites[0].Database)
	}
	if rep.Sites[0].Workers == nil {
		t.Error("workers must be [] (not null) in JSON")
	}
}
