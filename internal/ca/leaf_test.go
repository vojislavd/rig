package ca

import (
	"bytes"
	"crypto/x509"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestIssueLeafProperties(t *testing.T) {
	c, err := New(now)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := c.IssueLeaf("foo", now)
	if err != nil {
		t.Fatal(err)
	}
	cert := leaf.Cert

	if cert.IsCA {
		t.Error("leaf is marked as a CA")
	}
	want := []string{"foo.localhost", "*.foo.localhost"}
	if len(cert.DNSNames) != 2 || cert.DNSNames[0] != want[0] || cert.DNSNames[1] != want[1] {
		t.Errorf("DNSNames = %v, want %v", cert.DNSNames, want)
	}
	if len(cert.IPAddresses) != 0 {
		t.Errorf("IPAddresses = %v, want none", cert.IPAddresses)
	}
	if cert.KeyUsage != x509.KeyUsageDigitalSignature {
		t.Errorf("KeyUsage = %v, want DigitalSignature", cert.KeyUsage)
	}
	if len(cert.ExtKeyUsage) != 1 || cert.ExtKeyUsage[0] != x509.ExtKeyUsageServerAuth {
		t.Errorf("ExtKeyUsage = %v, want ServerAuth", cert.ExtKeyUsage)
	}
	if got := cert.NotAfter.Sub(now); got != leafValidity {
		t.Errorf("valid for %v, want %v", got, leafValidity)
	}
	if leaf.Key.Equal(c.Key) {
		t.Error("leaf reuses the CA key")
	}
}

func TestIssueLeafVerifies(t *testing.T) {
	c, err := New(now)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := c.IssueLeaf("foo", now)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(c.Cert)

	tests := []struct {
		host   string
		wantOK bool
	}{
		{"foo.localhost", true},
		{"branch.foo.localhost", true}, // worktree subdomain (v2)
		{"bar.localhost", false},       // another site
		{"a.b.foo.localhost", false},   // a wildcard covers one label only
		{"foo.com", false},
	}
	for _, tt := range tests {
		_, err := leaf.Cert.Verify(x509.VerifyOptions{Roots: roots, DNSName: tt.host, CurrentTime: now})
		if tt.wantOK && err != nil {
			t.Errorf("%s: verify failed: %v", tt.host, err)
		}
		if !tt.wantOK && err == nil {
			t.Errorf("%s: verify succeeded, want rejection", tt.host)
		}
	}
}

func TestIssueLeafRejectsBadNames(t *testing.T) {
	c, err := New(now)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"", "Foo", "rig", "../x", "foo.localhost", "*.foo"} {
		if _, err := c.IssueLeaf(name, now); err == nil {
			t.Errorf("IssueLeaf(%q) succeeded, want error", name)
		}
	}
}

func TestIssueLeafCappedByCAExpiry(t *testing.T) {
	// A CA created almost 10 years ago has only a few days left.
	c, err := New(now.Add(-validity + 10*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := c.IssueLeaf("foo", now)
	if err != nil {
		t.Fatal(err)
	}
	if !leaf.Cert.NotAfter.Equal(c.Cert.NotAfter) {
		t.Errorf("leaf expires %v, want capped to CA expiry %v", leaf.Cert.NotAfter, c.Cert.NotAfter)
	}
}

func TestIssueLeafExpiredCA(t *testing.T) {
	c, err := New(now.Add(-validity - time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.IssueLeaf("foo", now); err == nil {
		t.Error("IssueLeaf with an expired CA succeeded, want error")
	}
}

func TestLeafSave(t *testing.T) {
	c, err := New(now)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "certs")

	first, err := c.IssueLeaf("foo", now)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Save(dir, "foo"); err != nil {
		t.Fatal(err)
	}

	modes := map[string]os.FileMode{
		dir:                           0o700,
		filepath.Join(dir, "foo.key"): 0o600,
		filepath.Join(dir, "foo.crt"): 0o644,
	}
	for path, want := range modes {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != want {
			t.Errorf("%s mode = %o, want %o", path, got, want)
		}
	}

	// Saving a renewed certificate replaces the pair.
	renewed, err := c.IssueLeaf("foo", now)
	if err != nil {
		t.Fatal(err)
	}
	if err := renewed.Save(dir, "foo"); err != nil {
		t.Fatal(err)
	}
	assertSavedPair(t, dir, "foo", renewed)
}

func TestLeafSaveRejects(t *testing.T) {
	c, err := New(now)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := c.IssueLeaf("foo", now)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "certs")

	for _, name := range []string{"bar", "../foo", "Foo", ""} {
		if err := leaf.Save(dir, name); err == nil {
			t.Errorf("Save(%q) of a foo certificate succeeded, want error", name)
		}
	}
}

func TestLeafSaveRestoresKeyOnFailure(t *testing.T) {
	c, err := New(now)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "certs")
	first, err := c.IssueLeaf("foo", now)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Save(dir, "foo"); err != nil {
		t.Fatal(err)
	}
	oldKey, err := os.ReadFile(filepath.Join(dir, "foo.key"))
	if err != nil {
		t.Fatal(err)
	}

	// Make the cert write fail: a folder can't be replaced by a file.
	certPath := filepath.Join(dir, "foo.crt")
	if err := os.Remove(certPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(certPath, 0o700); err != nil {
		t.Fatal(err)
	}

	renewed, err := c.IssueLeaf("foo", now)
	if err != nil {
		t.Fatal(err)
	}
	if err := renewed.Save(dir, "foo"); err == nil {
		t.Fatal("Save succeeded, want error")
	}
	gotKey, err := os.ReadFile(filepath.Join(dir, "foo.key"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotKey, oldKey) {
		t.Error("old key was not restored after the failed save")
	}
}

// assertSavedPair checks the files on disk hold exactly leaf's cert and key.
func assertSavedPair(t *testing.T, dir, name string, leaf *Leaf) {
	t.Helper()
	certData, err := os.ReadFile(filepath.Join(dir, name+".crt"))
	if err != nil {
		t.Fatal(err)
	}
	keyData, err := os.ReadFile(filepath.Join(dir, name+".key"))
	if err != nil {
		t.Fatal(err)
	}
	cert, err := decodeCert(certData)
	if err != nil {
		t.Fatal(err)
	}
	key, err := decodeKey(keyData)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(cert.Raw, leaf.Cert.Raw) {
		t.Error("saved certificate differs")
	}
	if !key.Equal(leaf.Key) {
		t.Error("saved key differs")
	}
}
