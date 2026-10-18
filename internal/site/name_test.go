package site

import (
	"strings"
	"testing"
)

func TestValidateName(t *testing.T) {
	valid := []string{
		"foo",
		"foo-bar",
		"a",
		"0",
		"shop2",
		"a-b-c",
		strings.Repeat("a", 60), // exactly the limit
	}
	for _, name := range valid {
		if err := ValidateName(name); err != nil {
			t.Errorf("ValidateName(%q) = %v, want nil", name, err)
		}
	}

	invalid := []string{
		"",                      // empty
		strings.Repeat("a", 61), // over the limit
		"Foo",                   // uppercase
		"foo_bar",               // underscore (reserved for DB names)
		"foo.localhost",         // dot
		"-foo",                  // leading hyphen
		"foo-",                  // trailing hyphen
		"foo--bar",              // double hyphen
		"foo bar",               // space
		"foo;rm -rf ~",          // shell metacharacters
		"../etc",                // path traversal
		"foo/bar",               // slash
		"café",                  // non-ASCII
		"rig",                   // reserved
		"www",                   // reserved
	}
	for _, name := range invalid {
		if err := ValidateName(name); err == nil {
			t.Errorf("ValidateName(%q) = nil, want error", name)
		}
	}
}
