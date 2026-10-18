package ca

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"testing"
	"time"
)

var now = time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)

func TestNewProperties(t *testing.T) {
	c, err := New(now)
	if err != nil {
		t.Fatal(err)
	}
	cert := c.Cert

	if !cert.IsCA || !cert.BasicConstraintsValid {
		t.Error("not marked as a CA")
	}
	if cert.MaxPathLen != 0 || !cert.MaxPathLenZero {
		t.Errorf("MaxPathLen = %d (zero=%v), want 0: the CA must not sign other CAs", cert.MaxPathLen, cert.MaxPathLenZero)
	}
	if cert.KeyUsage != x509.KeyUsageCertSign|x509.KeyUsageCRLSign {
		t.Errorf("KeyUsage = %v, want CertSign|CRLSign only", cert.KeyUsage)
	}
	if len(cert.ExtKeyUsage) != 1 || cert.ExtKeyUsage[0] != x509.ExtKeyUsageServerAuth {
		t.Errorf("ExtKeyUsage = %v, want ServerAuth only", cert.ExtKeyUsage)
	}
	if !cert.PermittedDNSDomainsCritical {
		t.Error("name constraints not marked critical")
	}
	if len(cert.PermittedDNSDomains) != 1 || cert.PermittedDNSDomains[0] != "localhost" {
		t.Errorf("PermittedDNSDomains = %v, want [localhost]", cert.PermittedDNSDomains)
	}
	if len(cert.ExcludedIPRanges) != 2 {
		t.Errorf("ExcludedIPRanges = %v, want all IPv4 and IPv6", cert.ExcludedIPRanges)
	}
	if c.Key.Curve != elliptic.P256() {
		t.Error("key is not P-256")
	}
	if got := cert.NotAfter.Sub(now); got < 10*365*24*time.Hour-time.Hour {
		t.Errorf("valid for %v, want about 10 years", got)
	}
	if cert.NotBefore.After(now) {
		t.Errorf("NotBefore %v is after now", cert.NotBefore)
	}
}

func TestNewIsUnique(t *testing.T) {
	a, err := New(now)
	if err != nil {
		t.Fatal(err)
	}
	b, err := New(now)
	if err != nil {
		t.Fatal(err)
	}
	if a.Cert.SerialNumber.Cmp(b.Cert.SerialNumber) == 0 {
		t.Error("two CAs share a serial number")
	}
	if a.Key.Equal(b.Key) {
		t.Error("two CAs share a key")
	}
}

// TestNameConstraintsEnforced is the security check that matters: the CA
// must be useless for anything except .localhost names. Go's own verifier is
// used here; browsers are checked by hand in M1 step 5.
func TestNameConstraintsEnforced(t *testing.T) {
	c, err := New(now)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(c.Cert)

	tests := []struct {
		name   string
		dns    []string
		ips    []net.IP
		wantOK bool
	}{
		{"site", []string{"foo.localhost"}, nil, true},
		{"wildcard", []string{"*.foo.localhost"}, nil, true},
		{"real domain", []string{"example.com"}, nil, false},
		{"lookalike suffix", []string{"foolocalhost"}, nil, false},
		{"localhost as a middle label", []string{"localhost.example.com"}, nil, false},
		{"mixed good and bad", []string{"foo.localhost", "bank.com"}, nil, false},
		{"public IP", nil, []net.IP{net.ParseIP("1.2.3.4")}, false},
		{"loopback IP", nil, []net.IP{net.ParseIP("127.0.0.1")}, false},
		{"IPv6", nil, []net.IP{net.ParseIP("::1")}, false},
	}
	for _, tt := range tests {
		leaf := signLeaf(t, c, tt.dns, tt.ips)
		_, err := leaf.Verify(x509.VerifyOptions{Roots: roots, CurrentTime: now})
		if tt.wantOK && err != nil {
			t.Errorf("%s: verify failed: %v", tt.name, err)
		}
		if !tt.wantOK && err == nil {
			t.Errorf("%s: verify succeeded, want rejection", tt.name)
		}
	}
}

// signLeaf issues a test-only server certificate from c, bypassing any
// checks rig's real leaf code (step 2) will have.
func signLeaf(t *testing.T, c *CA, dns []string, ips []net.IP) *x509.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "leaf"},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     dns,
		IPAddresses:  ips,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, c.Cert, &key.PublicKey, c.Key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return leaf
}
