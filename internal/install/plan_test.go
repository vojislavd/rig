package install

import (
	"bytes"
	"io/fs"
	"strings"
	"testing"
	"time"

	"github.com/vojislavd/rig/internal/ca"
	"github.com/vojislavd/rig/internal/db"
)

func file(path string, mode fs.FileMode) Action {
	return Action{Kind: WriteFile, Why: "test", Path: path, Content: []byte("x"), Mode: mode}
}

func run(argv ...string) Action {
	return Action{Kind: Run, Why: "test", Argv: argv}
}

func TestValidateAccepts(t *testing.T) {
	p := &Plan{Actions: []Action{
		file("/etc/rig/dnsmasq.conf", 0o644),
		file("/etc/systemd/system/rig-dns.service", 0o644),
		file("/etc/rig/secret", 0o600),
		run("/usr/bin/systemctl", "daemon-reload"),
	}}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRejects(t *testing.T) {
	tests := []struct {
		name   string
		action Action
	}{
		{"outside allowlist", file("/etc/passwd", 0o644)},
		{"subfolder of /etc/rig", file("/etc/rig/sub/x.conf", 0o644)},
		{"dot-dot escape", file("/etc/rig/../passwd", 0o644)},
		{"relative path", file("etc/rig/x", 0o644)},
		{"non-rig unit", file("/etc/systemd/system/sshd.service", 0o644)},
		{"unit in a subfolder", file("/etc/systemd/system/rig-x.service.d/override.conf", 0o644)},
		{"resolved drop-in", file("/etc/systemd/resolved.conf.d/rig.conf", 0o644)},
		{"group-writable", file("/etc/rig/x", 0o664)},
		{"world-writable", file("/etc/rig/x", 0o646)},
		{"setuid", file("/etc/rig/x", 0o4755)},
		{"program not allowed", run("/usr/bin/bash", "-c", "id")},
		{"relative program", run("systemctl", "daemon-reload")},
		{"empty command", run()},
		{"missing reason", Action{Kind: Run, Argv: []string{"/usr/bin/systemctl"}}},
		{"unknown kind", Action{Kind: 99, Why: "test"}},
	}
	for _, tt := range tests {
		p := &Plan{Actions: []Action{tt.action}}
		if err := p.Validate(); err == nil {
			t.Errorf("%s: Validate = nil, want error", tt.name)
		}
	}
}

func TestRender(t *testing.T) {
	p := &Plan{Title: "rig install", Actions: []Action{
		{Kind: WriteFile, Why: "example file", Path: "/etc/rig/dnsmasq.conf", Content: []byte("abc"), Mode: 0o644},
		{Kind: Run, Why: "load units", Argv: []string{"/usr/bin/systemctl", "daemon-reload", "odd arg"}},
	}}
	var out bytes.Buffer
	p.Render(&out, false)
	got := out.String()

	for _, want := range []string{
		"1. write /etc/rig/dnsmasq.conf (root, 0644, 3 bytes)",
		"why: example file",
		`2. run   /usr/bin/systemctl daemon-reload "odd arg"`,
		"why: load units",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
}

func TestRenderEmpty(t *testing.T) {
	var out bytes.Buffer
	(&Plan{Title: "rig install"}).Render(&out, false)
	if !strings.Contains(out.String(), "nothing to do") {
		t.Errorf("got %q", out.String())
	}
}

func TestRenderShowFiles(t *testing.T) {
	p := &Plan{Actions: []Action{
		{Kind: WriteFile, Why: "w", Path: "/etc/rig/x", Content: []byte("line1\nline2\n"), Mode: 0o644},
	}}
	var out bytes.Buffer
	p.Render(&out, true)
	for _, want := range []string{"| line1\n", "| line2\n", "---- end ----"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output missing %q:\n%s", want, out.String())
		}
	}
}

func TestBuildIsValid(t *testing.T) {
	if err := Build(testInputs(t)).Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestDBSetupNeverShowsPassword(t *testing.T) {
	pw, _ := db.NewPassword()
	sql, _ := db.AdminSetupSQL(pw)
	p := Build(Inputs{CACert: testCADER(t), DBSetup: sql, DBSetupShown: db.Redact(sql, pw)})

	var out bytes.Buffer
	p.Render(&out, true)
	if strings.Contains(out.String(), pw) {
		t.Fatal("rendered plan contains the MariaDB password")
	}
	if !strings.Contains(out.String(), "GRANT EXECUTE ON PROCEDURE rigsys.create_site") {
		t.Error("--show does not print the SQL")
	}
	// The SQL goes to stdin, never into argv.
	for _, a := range p.Actions {
		if a.Kind == Run && a.Argv[0] == "/usr/bin/mariadb" {
			// Only the hash is sent, never the plaintext password.
			hash, _ := db.Hash(pw)
			if bytes.Contains(a.Stdin, []byte(pw)) {
				t.Error("stdin contains the plaintext password")
			}
			if !bytes.Contains(a.Stdin, []byte(hash)) {
				t.Error("stdin lacks the password hash")
			}
			for _, arg := range a.Argv {
				if strings.Contains(arg, pw) {
					t.Error("password in command-line arguments")
				}
			}
		}
	}
}

func TestBuildTrust(t *testing.T) {
	c, err := ca.New(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	p := BuildTrust(c.Cert.Raw)
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	if len(p.Actions) != 2 || p.Actions[0].Path != TrustAnchorPath || p.Actions[1].Argv[0] != updateCATrust {
		t.Errorf("plan = %+v", p.Actions)
	}
}
