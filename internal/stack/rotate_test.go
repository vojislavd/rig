package stack

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vojislavd/rig/internal/ca"
)

func TestRotateCA(t *testing.T) {
	st, _ := linkedStack(t)
	oldCA, err := ca.Load(st.L.CA)
	if err != nil {
		t.Fatal(err)
	}

	pending, isNew, err := st.PendingCA()
	if err != nil || !isNew {
		t.Fatalf("PendingCA = %v, %v", isNew, err)
	}
	if fileExists(st.pendingDir()) {
		t.Fatal("PendingCA saved before the user agreed")
	}
	if err := st.SavePendingCA(pending); err != nil {
		t.Fatal(err)
	}
	// A second run resumes with the same new CA.
	again, isNew, err := st.PendingCA()
	if err != nil || isNew || !again.Cert.Equal(pending.Cert) {
		t.Fatalf("resume: isNew=%v err=%v same=%v", isNew, err, again.Cert.Equal(pending.Cert))
	}

	// The old key leaked (loose mode): rotation must still work.
	os.Chmod(filepath.Join(st.L.CA, "ca.key"), 0o644)
	if err := st.SwapCA(); err != nil {
		t.Fatal(err)
	}
	current, err := ca.Load(st.L.CA)
	if err != nil || !current.Cert.Equal(pending.Cert) {
		t.Fatalf("current CA is not the new one (err %v)", err)
	}
	for _, gone := range []string{st.pendingDir(), st.L.CA + ".old"} {
		if fileExists(gone) {
			t.Errorf("%s still exists", gone)
		}
	}

	// Repair reissues the site certificate from the new CA.
	changes, err := st.Repair()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(changes, "\n"), "renewed certificate for foo") {
		t.Errorf("changes = %v", changes)
	}
	if s, _ := current.LeafStatus(st.L.Certs, "foo", st.Now()); s.Renew {
		t.Errorf("foo's certificate not valid under the new CA: %s", s.Reason)
	}
	if s, _ := oldCA.LeafStatus(st.L.Certs, "foo", st.Now()); !s.Renew {
		t.Error("foo's certificate still chains to the old CA")
	}
}

func TestSwapCARequiresPending(t *testing.T) {
	st, _ := linkedStack(t)
	if err := st.SwapCA(); err == nil {
		t.Fatal("SwapCA without a pending CA succeeded")
	}
	if _, err := ca.Load(st.L.CA); err != nil {
		t.Errorf("current CA damaged: %v", err)
	}
}
