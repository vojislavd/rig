package ca

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"syscall"

	"github.com/vojislavd/rig/internal/fsutil"
	"github.com/vojislavd/rig/internal/site"
)

// File names inside the CA folder (~/.local/share/rig/ca).
const (
	certFile = "ca.crt"
	keyFile  = "ca.key"
)

// maxPEMSize caps how much is read from a cert or key file. Real ones
// are well under 4 KiB.
const maxPEMSize = 64 << 10 // 64 KiB

// Save writes the CA to dir: ca.key (0600) and ca.crt (0644), with dir
// itself 0700. It refuses to overwrite an existing CA, because replacing
// it would break every certificate and trust entry made from the old one.
func (c *CA) Save(dir string) error {
	if err := fsutil.EnsurePrivateDir(dir); err != nil {
		return err
	}
	certPath := filepath.Join(dir, certFile)
	keyPath := filepath.Join(dir, keyFile)
	for _, p := range []string{certPath, keyPath} {
		if _, err := os.Lstat(p); !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("%s already exists, refusing to overwrite the CA", p)
		}
	}

	keyPEM, err := encodeKey(c.Key)
	if err != nil {
		return err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.Cert.Raw})

	// Key first: if writing the cert then fails, remove the key again so
	// no half-saved CA is left behind.
	if err := fsutil.WriteFileAtomic(keyPath, keyPEM, 0o600); err != nil {
		return err
	}
	// The certificate is public (it goes into the trust store), so 0644.
	if err := fsutil.WriteFileAtomic(certPath, certPEM, 0o644); err != nil {
		os.Remove(keyPath)
		return err
	}
	return nil
}

// Load reads the CA from dir and checks it before use. A missing CA
// returns an error that matches fs.ErrNotExist.
//
// Security: the folder and key must be private, the key must match the
// certificate, and the certificate must still carry the .localhost-only
// restrictions. rig never signs with a CA that fails any of these, even
// if the files were swapped or edited.
func Load(dir string) (*CA, error) {
	if err := fsutil.CheckPrivate(dir, true); err != nil {
		return nil, err
	}
	keyPath := filepath.Join(dir, keyFile)
	if err := fsutil.CheckPrivate(keyPath, false); err != nil {
		return nil, err
	}

	keyPEM, err := readSmallFile(keyPath)
	if err != nil {
		return nil, err
	}
	certPEM, err := readSmallFile(filepath.Join(dir, certFile))
	if err != nil {
		return nil, err
	}

	key, err := decodeKey(keyPEM)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", keyPath, err)
	}
	cert, err := decodeCert(certPEM)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Join(dir, certFile), err)
	}

	pub, ok := cert.PublicKey.(*ecdsa.PublicKey)
	if !ok || !pub.Equal(&key.PublicKey) {
		return nil, errors.New("CA key does not match CA certificate")
	}
	if err := checkRestricted(cert); err != nil {
		return nil, fmt.Errorf("CA certificate: %w", err)
	}
	return &CA{Cert: cert, Key: key}, nil
}

// ParseRestricted parses PEM data that must hold exactly one CA
// certificate with every restriction New gives a rig CA. rig install uses
// it to refuse trusting anything else.
func ParseRestricted(pemData []byte) (*x509.Certificate, error) {
	cert, err := decodeCert(pemData)
	if err != nil {
		return nil, err
	}
	if err := checkRestricted(cert); err != nil {
		return nil, err
	}
	return cert, nil
}

// checkRestricted confirms cert has the restrictions New gives a CA.
func checkRestricted(cert *x509.Certificate) error {
	switch {
	case !cert.IsCA || !cert.BasicConstraintsValid:
		return errors.New("not a CA")
	case cert.MaxPathLen != 0 || !cert.MaxPathLenZero:
		return errors.New("may sign sub-CAs (MaxPathLen is not 0)")
	case !cert.PermittedDNSDomainsCritical ||
		len(cert.PermittedDNSDomains) != 1 || cert.PermittedDNSDomains[0] != site.TLD:
		return errors.New("not name-constrained to ." + site.TLD)
	case !excludesAllIPs(cert.ExcludedIPRanges):
		return errors.New("does not exclude all IP addresses")
	}
	return nil
}

// excludesAllIPs reports whether ranges contain both 0.0.0.0/0 (all
// IPv4) and ::/0 (all IPv6).
func excludesAllIPs(ranges []*net.IPNet) bool {
	var v4, v6 bool
	for _, n := range ranges {
		ones, bits := n.Mask.Size()
		if ones != 0 {
			continue
		}
		switch bits {
		case 32:
			v4 = true
		case 128:
			v6 = true
		}
	}
	return v4 && v6
}

// encodeKey turns a private key into PEM text (PKCS #8, which nginx reads).
func encodeKey(key *ecdsa.PrivateKey) ([]byte, error) {
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("encode key: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), nil
}

func decodeKey(data []byte) (*ecdsa.PrivateKey, error) {
	der, err := decodePEM(data, "PRIVATE KEY")
	if err != nil {
		return nil, err
	}
	parsed, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		return nil, err
	}
	// A "type assertion": check the key really is ECDSA before using it.
	key, ok := parsed.(*ecdsa.PrivateKey)
	if !ok || key.Curve != elliptic.P256() {
		return nil, errors.New("key is not ECDSA P-256")
	}
	return key, nil
}

func decodeCert(data []byte) (*x509.Certificate, error) {
	der, err := decodePEM(data, "CERTIFICATE")
	if err != nil {
		return nil, err
	}
	return x509.ParseCertificate(der)
}

// decodePEM expects exactly one PEM block of the given type and nothing
// else. Headers are rejected: they only appear in old encrypted keys.
func decodePEM(data []byte, wantType string) ([]byte, error) {
	block, rest := pem.Decode(data)
	if block == nil {
		return nil, errors.New("no PEM data found")
	}
	if block.Type != wantType {
		return nil, fmt.Errorf("PEM type %q, want %q", block.Type, wantType)
	}
	if len(block.Headers) != 0 {
		return nil, errors.New("unexpected PEM headers")
	}
	if len(bytes.TrimSpace(rest)) != 0 {
		return nil, errors.New("unexpected data after the PEM block")
	}
	return block.Bytes, nil
}

// readSmallFile reads a file of at most maxPEMSize bytes without
// following a symlink at the final path element.
func readSmallFile(path string) ([]byte, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	data, err := io.ReadAll(io.LimitReader(f, maxPEMSize+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxPEMSize {
		return nil, fmt.Errorf("%s: larger than %d bytes", path, maxPEMSize)
	}
	return data, nil
}
