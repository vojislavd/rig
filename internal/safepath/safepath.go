// Package safepath checks paths before rig puts them into generated
// config files (nginx, php-fpm, systemd units).
package safepath

import (
	"fmt"
	"path/filepath"
	"regexp"
)

var plain = regexp.MustCompile(`^/[A-Za-z0-9/._-]*$`)

// Check accepts only clean absolute paths made of letters, digits and
// "/ . _ -".
//
// Security: config formats give meaning to characters like ";", "{", "#",
// "$", "%", "=", quotes, spaces and newlines. A folder name containing
// them could end a setting early and inject new ones. Plain paths can't.
func Check(p string) error {
	if !plain.MatchString(p) || filepath.Clean(p) != p {
		return fmt.Errorf("path %q is not allowed in generated config: use only letters, digits, / . _ -", p)
	}
	return nil
}
