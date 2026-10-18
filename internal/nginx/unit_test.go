package nginx

import (
	"strings"
	"testing"
)

func testUnitDirs() UnitDirs {
	return UnitDirs{
		Dirs: Dirs{
			Conf:    "/home/u/.config/rig/nginx",
			Logs:    "/home/u/.local/state/rig/logs",
			Runtime: "/run/user/1000/rig/nginx",
		},
		Code:  "/home/u/code",
		Certs: "/home/u/.local/share/rig/certs",
	}
}

func TestUnitContent(t *testing.T) {
	unit, err := Unit(testUnitDirs())
	if err != nil {
		t.Fatal(err)
	}
	s := string(unit)
	for _, want := range []string{
		"ExecStartPre=/usr/bin/nginx -e /home/u/.local/state/rig/logs/nginx-startup.log -c /home/u/.config/rig/nginx/nginx.conf -t -q\n",
		`ExecStart=/usr/bin/nginx -e /home/u/.local/state/rig/logs/nginx-startup.log -c /home/u/.config/rig/nginx/nginx.conf -g "daemon off;"` + "\n",
		"RuntimeDirectory=rig/nginx\n",
		"StateDirectory=rig/logs\n",
		"ExecReload=/usr/bin/kill -s HUP $MAINPID\n",
		"ProtectHome=tmpfs\n",
		"BindReadOnlyPaths=/home/u/code /home/u/.local/share/rig/certs /home/u/.config/rig/nginx\n",
		"BindPaths=/home/u/.local/state/rig/logs /run/user/1000/rig\n",
		"NoNewPrivileges=yes\n",
		"UMask=0077\n",
		"ProtectSystem=strict\n",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("unit missing %q", want)
		}
	}
	// The home folder must never be exposed as a whole.
	for _, banned := range []string{"ProtectHome=no", "ProtectHome=read-only", "BindPaths=/home/u\n", "BindReadOnlyPaths=/home/u\n"} {
		if strings.Contains(s, banned) {
			t.Errorf("unit contains %q", banned)
		}
	}
}

func TestUnitRejectsUnsafePaths(t *testing.T) {
	for _, bad := range []string{"/home/u/my code", "/home/u/%h", "/home/u/x\nExecStart=/bin/sh", "code"} {
		u := testUnitDirs()
		u.Code = bad
		if _, err := Unit(u); err == nil {
			t.Errorf("Unit accepted code dir %q", bad)
		}
	}
}

func TestUnitRequiresStandardFolders(t *testing.T) {
	u := testUnitDirs()
	u.Runtime = "/run/user/1000/elsewhere"
	if _, err := Unit(u); err == nil {
		t.Error("Unit accepted a runtime folder systemd would not create")
	}
}
