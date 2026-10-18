package install

import (
	"encoding/pem"
	"fmt"

	"github.com/vojislavd/rig/internal/ca"
)

// Trusting rig's CA. On Arch, the system trust
// store (p11-kit) also feeds NSS, so Chromium and Firefox see the CA too
// (/usr/lib/libnssckbi.so links to p11-kit-trust.so). One file covers
// browsers, curl, PHP and Go.
const (
	TrustAnchorPath = "/etc/ca-certificates/trust-source/anchors/rig-ca.crt"
	updateCATrust   = "/usr/bin/update-ca-trust"
)

// trustActions returns the steps that make the system trust caCert
// (the CA certificate, DER bytes).
func trustActions(caCertDER []byte) []Action {
	return []Action{
		{
			Kind: WriteFile, Path: TrustAnchorPath, Mode: 0o644,
			Content: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caCertDER}),
			Why:     "trust rig's CA (it can only sign *.localhost names), so https://<site>.localhost shows no warning",
		},
	}
}

// trustRefresh returns the step that rebuilds the trust store after the
// anchor file changes.
func trustRefresh() Action {
	return Action{
		Kind: Run, Argv: []string{updateCATrust},
		Why: "rebuild the system trust store so browsers, curl and PHP pick up the CA",
	}
}

// checkTrustAnchor makes sure only a restricted rig CA can ever be written
// to the trust store.
//
// Security: this is the most sensitive file rig install writes. Whatever
// is placed here, every program on the machine trusts. So beyond the
// path allowlist, the content itself must be exactly one certificate that
// passes ca.ParseRestricted (.localhost only, no IPs, no sub-CAs).
func checkTrustAnchor(content []byte) error {
	if _, err := ca.ParseRestricted(content); err != nil {
		return fmt.Errorf("%s: refusing to trust this certificate: %w", TrustAnchorPath, err)
	}
	return nil
}

// BuildTrust is the plan for "rig ca rotate": replace the trusted CA
// with caCertDER and rebuild the trust store. Writing the one anchor file
// replaces the old CA, so the old one stops being trusted in the same
// step.
func BuildTrust(caCertDER []byte) *Plan {
	p := &Plan{Title: "rig ca rotate"}
	p.Actions = append(trustActions(caCertDER), trustRefresh())
	return p
}
