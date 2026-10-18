package ca

import (
	"crypto/x509"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"time"

	"github.com/vojislavd/rig/internal/site"
)

// RenewBefore is how early a site certificate is renewed.
const RenewBefore = 30 * 24 * time.Hour

// LeafStatus describes a saved site certificate.
type LeafStatus struct {
	NotAfter time.Time // zero if there is no usable certificate
	Renew    bool      // true if a new certificate should be issued
	Reason   string    // why Renew is true, for messages
}

// LeafStatus checks the saved certificate for site name in dir against
// this CA. Renewal is needed if the certificate:
//   - is missing or unreadable
//   - was not signed by this CA (for example after a new CA was made)
//   - is not valid for <name>.localhost
//   - expires within RenewBefore
//
// An error is returned only for problems renewal can't fix, such as an
// invalid site name or a folder rig can't read.
func (c *CA) LeafStatus(dir, name string, now time.Time) (LeafStatus, error) {
	if err := site.ValidateName(name); err != nil {
		return LeafStatus{}, err
	}

	data, err := readSmallFile(filepath.Join(dir, name+".crt"))
	if errors.Is(err, fs.ErrNotExist) {
		return LeafStatus{Renew: true, Reason: "no certificate"}, nil
	}
	if err != nil {
		return LeafStatus{}, err
	}
	cert, err := decodeCert(data)
	if err != nil {
		return LeafStatus{Renew: true, Reason: "certificate unreadable: " + err.Error()}, nil
	}

	// Verify the way a browser would: chain to this CA, host name, and
	// validity at the current time.
	roots := x509.NewCertPool()
	roots.AddCert(c.Cert)
	opts := x509.VerifyOptions{Roots: roots, DNSName: site.Host(name), CurrentTime: now}
	if _, err := cert.Verify(opts); err != nil {
		return LeafStatus{NotAfter: cert.NotAfter, Renew: true, Reason: "not valid: " + err.Error()}, nil
	}

	if left := cert.NotAfter.Sub(now); left < RenewBefore {
		days := int(left.Hours() / 24)
		return LeafStatus{NotAfter: cert.NotAfter, Renew: true, Reason: fmt.Sprintf("expires in %d days", days)}, nil
	}
	return LeafStatus{NotAfter: cert.NotAfter}, nil
}
