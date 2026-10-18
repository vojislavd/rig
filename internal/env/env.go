// Package env edits Laravel .env files, changing only the keys rig
// manages and leaving every other line as it was.
package env

import (
	"fmt"
	"regexp"
	"strings"
)

// Var is one key rig sets.
type Var struct {
	Key    string
	Value  string
	Secret bool // never shown in output
	// OnlyIfEmpty: set the key only if it is missing or empty; an existing
	// value is kept (e.g. generated app credentials).
	OnlyIfEmpty bool
	// HideOld: the new value may be shown, but the old one is hidden
	// (e.g. replacing a real SMTP password with "null").
	HideOld bool
}

// Change describes what happens to one key.
type Change struct {
	Key    string `json:"key"`
	Old    string `json:"old"`   // "" if missing; "(hidden)" for secrets
	New    string `json:"new"`   // "(hidden)" for secrets
	Before string `json:"state"` // "set", "commented" or "missing"
}

// safeValue limits values to characters that need no quoting, so a value
// can never carry a newline, quote or "#" that would change the file's
// meaning.
var safeValue = regexp.MustCompile(`^[A-Za-z0-9_.:/@+-]*$`)

var safeKey = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

// Apply returns content with every var set, plus the list of changes.
// For each key: the first active "KEY=" line is replaced; otherwise a
// commented "# KEY=" line is replaced (uncommented in place, as in
// Laravel's default .env); otherwise the key is appended at the end
// under a "# rig" comment. All other lines are kept byte for byte.
func Apply(content []byte, vars []Var) ([]byte, []Change, error) {
	lines := strings.Split(string(content), "\n")
	trailingNewline := strings.HasSuffix(string(content), "\n") || len(content) == 0
	if trailingNewline && len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	var changes []Change
	var appended []string
	for _, v := range vars {
		if !safeKey.MatchString(v.Key) {
			return nil, nil, fmt.Errorf("invalid key %q", v.Key)
		}
		if !safeValue.MatchString(v.Value) {
			return nil, nil, fmt.Errorf("%s: value contains characters rig won't write to .env", v.Key)
		}
		newLine := v.Key + "=" + v.Value

		active := regexp.MustCompile(`^\s*(export\s+)?` + regexp.QuoteMeta(v.Key) + `\s*=(.*)$`)
		commented := regexp.MustCompile(`^\s*#\s*` + regexp.QuoteMeta(v.Key) + `\s*=(.*)$`)

		idx, state, old := -1, "missing", ""
		for i, l := range lines {
			if m := active.FindStringSubmatch(l); m != nil {
				idx, state, old = i, "set", unquote(m[2])
				break
			}
		}
		if idx < 0 {
			for i, l := range lines {
				if m := commented.FindStringSubmatch(l); m != nil {
					idx, state, old = i, "commented", unquote(m[1])
					break
				}
			}
		}

		if state == "set" && old == v.Value {
			continue // already right
		}
		if v.OnlyIfEmpty && state == "set" && old != "" {
			continue // keep the existing value
		}
		if idx >= 0 {
			lines[idx] = newLine
		} else {
			appended = append(appended, newLine)
		}
		c := Change{Key: v.Key, Old: old, New: v.Value, Before: state}
		if (v.Secret || v.HideOld) && c.Old != "" {
			c.Old = "(hidden)"
		}
		if v.Secret {
			c.New = "(hidden)"
		}
		changes = append(changes, c)
	}

	if len(appended) > 0 {
		if len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) != "" {
			lines = append(lines, "")
		}
		lines = append(lines, "# rig")
		lines = append(lines, appended...)
	}
	out := strings.Join(lines, "\n")
	if len(lines) > 0 {
		out += "\n"
	}
	return []byte(out), changes, nil
}

// unquote strips surrounding quotes and spaces from a value for display.
func unquote(v string) string {
	v = strings.TrimSpace(v)
	if len(v) >= 2 && (v[0] == '"' && v[len(v)-1] == '"' || v[0] == '\'' && v[len(v)-1] == '\'') {
		return v[1 : len(v)-1]
	}
	return v
}
