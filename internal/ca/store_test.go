package ca

import (
	"bytes"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"io/fs"
	"math/big"
	"os"
	"path/filepath"
	"testing"
)

// savedCA creates a CA, saves it to a fresh folder and returns both.
func savedCA(t *testing.T) (*CA, string) {
	t.Helper()
	c, err := New(now)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "ca")
	if err := c.Save(dir); err != nil {
		t.Fatal(err)
	}
	return c, dir
}

func TestSaveLoadRoundTrip(t *testing.T) {
	c, dir := savedCA(t)

	modes := map[string]os.FileMode{
		dir:                          0o700,
		filepath.Join(dir, keyFile):  0o600,
		filepath.Join(dir, certFile): 0o644,
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

	loaded, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(loaded.Cert.Raw, c.Cert.Raw) {
		t.Error("loaded certificate differs")
	}
	if !loaded.Key.Equal(c.Key) {
		t.Error("loaded key differs")
	}
}

func TestSaveRefusesOverwrite(t *testing.T) {
	_, dir := savedCA(t)
	other, err := New(now)
	if err != nil {
		t.Fatal(err)
	}
	if err := other.Save(dir); err == nil {
		t.Error("Save over an existing CA succeeded, want error")
	}
}

func TestLoadMissing(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "ca"))
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Load of a missing CA = %v, want fs.ErrNotExist", err)
	}
}

func TestLoadRejectsUnsafeFiles(t *testing.T) {
	tests := []struct {
		name   string
		damage func(t *testing.T, dir string)
	}{
		{"group-readable key", func(t *testing.T, dir string) {
			chmod(t, filepath.Join(dir, keyFile), 0o640)
		}},
		{"open folder", func(t *testing.T, dir string) {
			chmod(t, dir, 0o755)
		}},
		{"key is a symlink", func(t *testing.T, dir string) {
			key := filepath.Join(dir, keyFile)
			moved := filepath.Join(dir, "real.key")
			if err := os.Rename(key, moved); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(moved, key); err != nil {
				t.Fatal(err)
			}
		}},
		{"cert is a symlink", func(t *testing.T, dir string) {
			cert := filepath.Join(dir, certFile)
			moved := filepath.Join(dir, "real.crt")
			if err := os.Rename(cert, moved); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(moved, cert); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, tt := range tests {
		_, dir := savedCA(t)
		tt.damage(t, dir)
		if _, err := Load(dir); err == nil {
			t.Errorf("%s: Load succeeded, want error", tt.name)
		}
	}
}

func TestLoadRejectsBadContent(t *testing.T) {
	otherCA, err := New(now)
	if err != nil {
		t.Fatal(err)
	}
	otherKey, err := encodeKey(otherCA.Key)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		file string
		data func(c *CA) []byte
	}{
		{"key from another CA", keyFile, func(*CA) []byte { return otherKey }},
		{"garbage key", keyFile, func(*CA) []byte { return []byte("nope") }},
		{"cert in the key file", keyFile, func(c *CA) []byte { return certPEM(c.Cert.Raw) }},
		{"garbage cert", certFile, func(*CA) []byte { return []byte("nope") }},
		{"trailing data", certFile, func(c *CA) []byte { return append(certPEM(c.Cert.Raw), "junk"...) }},
		{"two certs", certFile, func(c *CA) []byte { return append(certPEM(c.Cert.Raw), certPEM(c.Cert.Raw)...) }},
		{"PEM headers", certFile, func(c *CA) []byte {
			return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Headers: map[string]string{"Proc-Type": "4,ENCRYPTED"}, Bytes: c.Cert.Raw})
		}},
		// The dangerous case: same key, but a certificate without the
		// .localhost restriction. rig must refuse to sign with it.
		{"unconstrained cert", certFile, func(c *CA) []byte { return certPEM(unconstrainedCert(t, c)) }},
		{"too big", certFile, func(*CA) []byte { return bytes.Repeat([]byte(" "), maxPEMSize+1) }},
	}
	for _, tt := range tests {
		c, dir := savedCA(t)
		mode := os.FileMode(0o644)
		if tt.file == keyFile {
			mode = 0o600
		}
		if err := os.WriteFile(filepath.Join(dir, tt.file), tt.data(c), mode); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(dir); err == nil {
			t.Errorf("%s: Load succeeded, want error", tt.name)
		}
	}
}

func certPEM(der []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

// unconstrainedCert self-signs a CA certificate with c's key but none of
// the restrictions.
func unconstrainedCert(t *testing.T, c *CA) []byte {
	t.Helper()
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(2),
		Subject:               pkix.Name{CommonName: "evil"},
		NotBefore:             now,
		NotAfter:              now.Add(validity),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &c.Key.PublicKey, c.Key)
	if err != nil {
		t.Fatal(err)
	}
	return der
}

func chmod(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}
