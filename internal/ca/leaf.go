package ca

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/vojislavd/rig/internal/fsutil"
	"github.com/vojislavd/rig/internal/site"
)

// leafValidity is how long a site certificate lasts: 397 days, the
// maximum Apple and Chrome accept for TLS server certificates.
const leafValidity = 397 * 24 * time.Hour

// Leaf is a site's HTTPS certificate and its private key.
type Leaf struct {
	Cert *x509.Certificate
	Key  *ecdsa.PrivateKey
}

// IssueLeaf creates a certificate for <name>.localhost and *.<name>.localhost,
// with its own new key.
//
// The site name is validated here too, even though callers should have
// done it already: this is the last stop before something gets signed.
func (c *CA) IssueLeaf(name string, now time.Time) (*Leaf, error) {
	if err := site.ValidateName(name); err != nil {
		return nil, err
	}
	if !now.Before(c.Cert.NotAfter) {
		return nil, fmt.Errorf("CA expired on %s", c.Cert.NotAfter.Format(time.DateOnly))
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate key for %s: %w", name, err)
	}
	serial, err := randomSerial()
	if err != nil {
		return nil, err
	}

	// A certificate can't outlive the CA that signed it, so cap the end
	// date. Renewal (step 4) then sees the real expiry.
	notAfter := now.Add(leafValidity)
	if notAfter.After(c.Cert.NotAfter) {
		notAfter = c.Cert.NotAfter
	}

	host := site.Host(name)
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: host},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              notAfter,
		BasicConstraintsValid: true, // IsCA stays false
		// ECDSA keys only sign, so DigitalSignature is the only usage.
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:    []string{host, "*." + host},
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, c.Cert, &key.PublicKey, c.Key)
	if err != nil {
		return nil, fmt.Errorf("sign certificate for %s: %w", host, err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("parse certificate for %s: %w", host, err)
	}
	return &Leaf{Cert: cert, Key: key}, nil
}

// Save writes the certificate to dir as <name>.crt (0644) and its key as
// <name>.key (0600), replacing any previous pair (renewal). dir is
// created as 0700 if missing.
//
// name must be the site this certificate was issued for: the file names
// are built from it, and a mismatch would serve the wrong certificate.
func (l *Leaf) Save(dir, name string) error {
	if err := site.ValidateName(name); err != nil {
		return err
	}
	if len(l.Cert.DNSNames) == 0 || l.Cert.DNSNames[0] != site.Host(name) {
		return fmt.Errorf("certificate was not issued for %s", site.Host(name))
	}
	if err := fsutil.EnsurePrivateDir(dir); err != nil {
		return err
	}

	keyPEM, err := encodeKey(l.Key)
	if err != nil {
		return err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: l.Cert.Raw})
	keyPath := filepath.Join(dir, name+".key")
	certPath := filepath.Join(dir, name+".crt")

	// Remember the old key, so a failed cert write can put it back.
	// Otherwise nginx would see a new key next to the old certificate.
	oldKey, err := readSmallFile(keyPath)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}

	if err := fsutil.WriteFileAtomic(keyPath, keyPEM, 0o600); err != nil {
		return err
	}
	if err := fsutil.WriteFileAtomic(certPath, certPEM, 0o644); err != nil {
		if oldKey != nil {
			fsutil.WriteFileAtomic(keyPath, oldKey, 0o600)
		} else {
			os.Remove(keyPath)
		}
		return err
	}
	return nil
}
