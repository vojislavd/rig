// Package ca creates and uses rig's local certificate authority, which
// signs the HTTPS certificates for *.localhost sites.
package ca

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"math/big"
	"net"
	"time"

	"github.com/vojislavd/rig/internal/site"
)

// validity is how long the CA certificate lasts.
const validity = 10 * 365 * 24 * time.Hour

// CA is a certificate authority: its certificate and private key.
type CA struct {
	Cert *x509.Certificate
	Key  *ecdsa.PrivateKey
}

// New creates a fresh CA, valid from now for 10 years.
//
// Security: the browser will trust this CA, so a stolen CA key must be
// useless against real sites. The certificate is therefore restricted:
//   - Name constraints, marked critical: it may only sign names under
//     .localhost, and no IP addresses at all. A verifier that doesn't
//     understand the constraints must reject the certificate.
//   - MaxPathLen 0: it can't create sub-CAs that might escape the limits.
//   - Extended key usage ServerAuth only: not usable for code signing or
//     email (S/MIME).
func New(now time.Time) (*CA, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate CA key: %w", err)
	}
	serial, err := randomSerial()
	if err != nil {
		return nil, err
	}

	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   "rig local CA (.localhost only)",
			Organization: []string{"rig"},
		},
		// Start slightly in the past to tolerate small clock differences.
		NotBefore: now.Add(-time.Hour),
		NotAfter:  now.Add(validity),

		IsCA:                  true,
		BasicConstraintsValid: true,
		MaxPathLen:            0,
		MaxPathLenZero:        true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},

		// "localhost" covers localhost itself and every name ending in
		// .localhost.
		PermittedDNSDomainsCritical: true,
		PermittedDNSDomains:         []string{site.TLD},
		ExcludedIPRanges: []*net.IPNet{
			{IP: net.IPv4zero, Mask: net.CIDRMask(0, 32)},         // all IPv4
			{IP: net.IPv6unspecified, Mask: net.CIDRMask(0, 128)}, // all IPv6
		},
	}

	// A CA signs its own certificate: template and parent are the same.
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, fmt.Errorf("create CA certificate: %w", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("parse CA certificate: %w", err)
	}
	return &CA{Cert: cert, Key: key}, nil
}

// randomSerial returns a random 128-bit serial number. Random serials
// keep every certificate unique, as RFC 5280 and browsers expect.
func randomSerial() (*big.Int, error) {
	limit := new(big.Int).Lsh(big.NewInt(1), 128)
	n, err := rand.Int(rand.Reader, limit)
	if err != nil {
		return nil, fmt.Errorf("generate serial: %w", err)
	}
	return n, nil
}
