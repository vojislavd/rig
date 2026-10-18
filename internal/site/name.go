// Package site manages the registry of sites and validates their names.
package site

import (
	"fmt"
	"regexp"
)

// MaxNameLen keeps the database name "rig_<site>" within MariaDB's
// 64-character limit.
const MaxNameLen = 60

// namePattern allows lowercase letters and digits, optionally joined by
// single hyphens: no leading, trailing or double hyphens, so every name is
// a valid DNS label. Go's regexp package runs in linear time, so a hostile
// input can't make matching slow.
var namePattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// TLD is the top-level domain for every site: foo is served as
// foo.localhost. systemd-resolved and browsers already send *.localhost to
// this machine (RFC 6761), so rig needs no DNS server.
const TLD = "localhost"

// Host returns the host name for a site: "foo" -> "foo.localhost".
func Host(name string) string {
	return name + "." + TLD
}

// reserved names are used by rig itself (rig.localhost) or are confusing as sites.
var reserved = map[string]bool{
	"rig": true,
	"www": true,
}

// ValidateName reports whether name is an acceptable site name.
//
// Security: a site name ends up in file paths, nginx config, systemd unit
// names and SQL identifiers. Only names that pass this check may be used
// anywhere, so none of those places ever sees "..", "/", spaces, quotes or
// shell characters.
func ValidateName(name string) error {
	// Check the length first, so the pattern never runs on huge input.
	if len(name) == 0 || len(name) > MaxNameLen {
		return fmt.Errorf("site name must be 1-%d characters", MaxNameLen)
	}
	if !namePattern.MatchString(name) {
		return fmt.Errorf("invalid site name %q: use lowercase letters, digits and single hyphens", name)
	}
	if reserved[name] {
		return fmt.Errorf("site name %q is reserved", name)
	}
	return nil
}
