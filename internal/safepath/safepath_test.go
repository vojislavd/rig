package safepath

import "testing"

func TestCheck(t *testing.T) {
	for _, ok := range []string{"/", "/home/u/code", "/run/user/1000/rig/php85", "/a/b.c/d_e-f"} {
		if err := Check(ok); err != nil {
			t.Errorf("Check(%q) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []string{
		"", "relative", "/home/u/my code", "/x;y", "/x{", "/$v", "/x#c", "/%h",
		"/a=b", "/'q'", `/"q"`, "/a/../b", "/a/", "/x\ny", "/café",
	} {
		if err := Check(bad); err == nil {
			t.Errorf("Check(%q) = nil, want error", bad)
		}
	}
}
