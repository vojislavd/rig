package install

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"slices"
	"testing"
	"time"

	"github.com/vojislavd/rig/internal/ca"
	"github.com/vojislavd/rig/internal/db"
)

// testCADER returns the DER bytes of a fresh, properly restricted rig CA.
func testCADER(t *testing.T) []byte {
	t.Helper()
	c, err := ca.New(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return c.Cert.Raw
}

// testInputs returns valid install inputs with a fresh CA and DB password.
func testInputs(t *testing.T) Inputs {
	t.Helper()
	pw, err := db.NewPassword()
	if err != nil {
		t.Fatal(err)
	}
	sql, err := db.AdminSetupSQL(pw)
	if err != nil {
		t.Fatal(err)
	}
	return Inputs{CACert: testCADER(t), DBSetup: sql, DBSetupShown: db.Redact(sql, pw)}
}

func anchor(content []byte) Action {
	return Action{Kind: WriteFile, Why: "test", Path: TrustAnchorPath, Mode: 0o644, Content: content}
}

func pemCert(der []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

// unrestrictedCADER is a CA that could sign any website: the thing rig
// install must never trust.
func unrestrictedCADER(t *testing.T) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "evil CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return der
}

func TestTrustAnchorAccepted(t *testing.T) {
	p := &Plan{Actions: []Action{anchor(pemCert(testCADER(t)))}}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestTrustAnchorRejected(t *testing.T) {
	good := pemCert(testCADER(t))
	tests := []struct {
		name    string
		content []byte
	}{
		{"unrestricted CA", pemCert(unrestrictedCADER(t))},
		{"two certificates", append(append([]byte{}, good...), good...)},
		{"garbage", []byte("not a certificate")},
		{"empty", nil},
	}
	for _, tt := range tests {
		p := &Plan{Actions: []Action{anchor(tt.content)}}
		if err := p.Validate(); err == nil {
			t.Errorf("%s: Validate = nil, want error", tt.name)
		}
	}
}

func TestBuildTrustsCA(t *testing.T) {
	in := testInputs(t)
	der := in.CACert
	p := Build(in)
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}

	var anchorAt, refreshAt, reloadAt = -1, -1, -1
	for i, a := range p.Actions {
		switch {
		case a.Kind == WriteFile && a.Path == TrustAnchorPath:
			anchorAt = i
			if string(a.Content) != string(pemCert(der)) {
				t.Error("anchor content is not the given CA")
			}
		case a.Kind == Run && slices.Equal(a.Argv, []string{updateCATrust}):
			refreshAt = i
		case a.Kind == Run && slices.Contains(a.Argv, "daemon-reload"):
			reloadAt = i
		}
	}
	if anchorAt < 0 || refreshAt < 0 {
		t.Fatalf("plan lacks the anchor (%d) or update-ca-trust (%d)", anchorAt, refreshAt)
	}
	if !(anchorAt < refreshAt && refreshAt < reloadAt) {
		t.Errorf("order anchor=%d refresh=%d reload=%d, want anchor < refresh < reload", anchorAt, refreshAt, reloadAt)
	}
}
