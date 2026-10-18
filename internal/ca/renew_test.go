package ca

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLeafStatus(t *testing.T) {
	c, err := New(now)
	if err != nil {
		t.Fatal(err)
	}
	other, err := New(now)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "certs")
	day := 24 * time.Hour

	save := func(issuer *CA, name string, at time.Time) {
		t.Helper()
		leaf, err := issuer.IssueLeaf(name, at)
		if err != nil {
			t.Fatal(err)
		}
		if err := leaf.Save(dir, name); err != nil {
			t.Fatal(err)
		}
	}

	save(c, "fresh", now)                               // 397 days left
	save(c, "old", now.Add(-leafValidity+10*day))       // 10 days left
	save(c, "edge", now.Add(-leafValidity+RenewBefore)) // exactly 30 days left
	save(c, "expired", now.Add(-leafValidity-day))      // expired yesterday
	save(other, "foreign", now)                         // signed by another CA
	save(c, "copied", now)                              // will be copied to "wrong"
	if err := os.WriteFile(filepath.Join(dir, "garbage.crt"), []byte("nope"), 0o644); err != nil {
		t.Fatal(err)
	}
	// wrong.crt holds the certificate for copied.localhost.
	data, err := os.ReadFile(filepath.Join(dir, "copied.crt"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "wrong.crt"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name      string
		wantRenew bool
	}{
		{"fresh", false},
		{"edge", false},
		{"old", true},
		{"expired", true},
		{"foreign", true},
		{"wrong", true},
		{"garbage", true},
		{"missing", true},
	}
	for _, tt := range tests {
		st, err := c.LeafStatus(dir, tt.name, now)
		if err != nil {
			t.Errorf("%s: %v", tt.name, err)
			continue
		}
		if st.Renew != tt.wantRenew {
			t.Errorf("%s: Renew = %v (%s), want %v", tt.name, st.Renew, st.Reason, tt.wantRenew)
		}
		if st.Renew && st.Reason == "" {
			t.Errorf("%s: Renew without a reason", tt.name)
		}
	}

	if _, err := c.LeafStatus(dir, "../etc", now); err == nil {
		t.Error("LeafStatus with an invalid name succeeded, want error")
	}
}
