package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vojislavd/rig/internal/install"
	"github.com/vojislavd/rig/internal/stack"
)

func TestRun(t *testing.T) {
	// Point rig's data folder at an empty temp folder, so the tests never
	// read the real CA in ~/.local/share/rig.
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	// Answer "no" to every confirmation, and fail loudly if anything
	// tries to apply a plan: these tests must never run sudo.
	stdin = strings.NewReader("no\n")
	newApplier = func(io.Writer) *install.Applier {
		t.Fatal("a plan was applied during tests")
		return nil
	}
	// Real folders come from the temp XDG variables above; systemd is a
	// stand-in that fails the test if anything tries to use it.
	newStack = func() (*stack.Stack, error) {
		st, err := stack.New()
		if err != nil {
			return nil, err
		}
		st.S = noSystemd{t}
		return st, nil
	}

	// A "table-driven" test: each row is one case, and the loop below
	// runs them all the same way.
	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStdout string // text stdout must contain ("" = must be empty)
		wantStderr string // text stderr must contain ("" = must be empty)
	}{
		{"version", []string{"version"}, 0, "rig " + version, ""},
		{"help", []string{"help"}, 0, "usage: rig", ""},
		{"no args", nil, 2, "", "usage: rig"},
		{"unknown", []string{"nope"}, 2, "", `unknown command "nope"`},
		{"install dry-run", []string{"install", "--dry-run"}, 0, "/etc/rig/nat.nft", ""},
		{"install dry-run show", []string{"install", "--dry-run", "--show"}, 0, "redirect to :8443", ""},
		{"install dry-run trusts CA", []string{"install", "--dry-run"}, 0, "rig-ca.crt", ""},
		{"install answered no", []string{"install"}, 1, "[y/N]", "aborted, nothing changed"},
		{"uninstall dry-run", []string{"uninstall", "--dry-run"}, 0, "stop trusting rig's CA", ""},
		{"uninstall answered no", []string{"uninstall"}, 1, "[y/N]", "aborted, nothing changed"},
		{"sites empty", []string{"sites"}, 0, "No sites yet", ""},
		{"status no-db extra arg", []string{"status", "--no-db", "x"}, 2, "", `unexpected argument "x"`},
		{"status extra arg", []string{"status", "x"}, 2, "", `unexpected argument "x"`},
		{"logs no target", []string{"logs"}, 2, "", "usage: rig logs <target>"},
		{"logs unknown site", []string{"logs", "nope"}, 1, "", "site not found"},
		{"logs bad target", []string{"logs", "../x"}, 1, "", "unknown log"},
		{"logs extra arg", []string{"logs", "nginx", "php"}, 2, "", `unexpected argument "php"`},
		{"logs bad count", []string{"logs", "nginx", "-n", "-1"}, 2, "", "-n must be"},
		{"ca no subcommand", []string{"ca"}, 2, "", "usage: rig ca rotate"},
		{"ca rotate dry-run", []string{"ca", "rotate", "--dry-run"}, 0, "rig-ca.crt", ""},
		{"ca rotate answered no", []string{"ca", "rotate"}, 1, "[y/N]", "aborted, nothing changed"},
		{"doctor extra arg", []string{"doctor", "x"}, 2, "", `unexpected argument "x"`},
		{"sites empty json", []string{"sites", "--json"}, 0, "[]", ""},
		{"unlink unknown", []string{"unlink", "nope"}, 1, "", "site not found"},
		{"unlink bad name", []string{"unlink", "../etc"}, 1, "", "invalid site name"},
		{"open unknown", []string{"open", "nope"}, 1, "", "not a linked site"},
		{"link extra arg", []string{"link", "a", "b"}, 2, "", `unexpected argument "b"`},
		{"env json without yes", []string{"env", "--json"}, 2, "", "--json needs --yes"},
		{"env unknown site", []string{"env", "nope"}, 1, "", "not a linked site"},
		{"rm no site", []string{"rm"}, 2, "", "usage: rig rm"},
		{"rm purge with yes", []string{"rm", "foo", "--purge", "--yes"}, 2, "", "must type the site name"},
		{"rm json without yes", []string{"rm", "foo", "--json"}, 2, "", "--json needs --yes"},
		{"worker no args", []string{"worker"}, 2, "", "usage: rig worker"},
		{"worker list empty", []string{"worker", "list"}, 0, "No workers running", ""},
		{"worker start unknown site", []string{"worker", "start", "nope", "queue"}, 1, "", "site not found"},
		{"worker start bad kind", []string{"worker", "start", "nope", "tinker"}, 1, "", "unknown worker"},
		{"worker start missing kind", []string{"worker", "start", "nope"}, 2, "", "usage: rig worker"},
		{"worker stop nothing", []string{"worker", "stop"}, 2, "", "usage: rig worker"},
		{"mail no args", []string{"mail"}, 2, "", "usage: rig mail"},
		{"mail unknown", []string{"mail", "nope"}, 2, "", `unknown command "nope"`},
		{"s3 no args", []string{"s3"}, 2, "", "usage: rig s3"},
		{"s3 unknown", []string{"s3", "nope"}, 2, "", `unknown command "nope"`},
		{"s3 enable no site", []string{"s3", "enable"}, 2, "", "usage: rig s3"},
		{"s3 start extra", []string{"s3", "start", "x"}, 2, "", "usage: rig s3"},
		{"s3 put missing args", []string{"s3", "put", "foo", "a"}, 2, "", "usage: rig s3"},
		{"s3 rm too many", []string{"s3", "rm", "foo", "a", "b"}, 2, "", "usage: rig s3"},
		{"db no args", []string{"db"}, 2, "", "usage: rig db"},
		{"db unknown", []string{"db", "nope"}, 2, "", `unknown command "nope"`},
		{"db list without install", []string{"db", "list"}, 1, "", "run rig install first"},
		{"db dump no site", []string{"db", "dump"}, 2, "", "usage: rig db"},
		{"db drop no site", []string{"db", "drop"}, 2, "", "usage: rig db"},
		{"db drop bad name", []string{"db", "drop", "../x"}, 1, "", "invalid site name"},
		{"db drop json without yes", []string{"db", "drop", "x", "--json"}, 2, "", "--json needs --yes"},
		{"install bad flag", []string{"install", "--nope"}, 2, "", "flag provided but not defined"},
		{"install extra arg", []string{"install", "now"}, 2, "", `unexpected argument "now"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// bytes.Buffer collects whatever run prints, so we can check it.
			var stdout, stderr bytes.Buffer

			code := run(tt.args, &stdout, &stderr)

			if code != tt.wantCode {
				t.Errorf("exit code = %d, want %d", code, tt.wantCode)
			}
			checkOutput(t, "stdout", stdout.String(), tt.wantStdout)
			checkOutput(t, "stderr", stderr.String(), tt.wantStderr)
		})
	}
}

func checkOutput(t *testing.T, stream, got, want string) {
	t.Helper() // report failures at the caller's line, not here
	if want == "" {
		if got != "" {
			t.Errorf("%s = %q, want empty", stream, got)
		}
		return
	}
	if !strings.Contains(got, want) {
		t.Errorf("%s = %q, want it to contain %q", stream, got, want)
	}
}

func TestInstallNoDoesNotSaveCA(t *testing.T) {
	data := t.TempDir()
	t.Setenv("XDG_DATA_HOME", data)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	stdin = strings.NewReader("no\n")
	var out, errOut bytes.Buffer
	if code := run([]string{"install"}, &out, &errOut); code != 1 {
		t.Fatalf("exit code %d, want 1", code)
	}
	if _, err := os.Stat(filepath.Join(data, "rig")); !os.IsNotExist(err) {
		t.Error("answering no still created files in the data folder")
	}
	cfg := os.Getenv("XDG_CONFIG_HOME")
	if _, err := os.Stat(filepath.Join(cfg, "rig", "mariadb.cnf")); !os.IsNotExist(err) {
		t.Error("answering no still saved the MariaDB login")
	}
}

func TestConfirm(t *testing.T) {
	tests := map[string]bool{
		"y\n": true, "Y\n": true, "yes\n": true, " YES \n": true,
		"\n": false, "n\n": false, "no\n": false, "yep\n": false, "": false,
	}
	for input, want := range tests {
		stdin = strings.NewReader(input)
		if got := confirm(io.Discard); got != want {
			t.Errorf("confirm(%q) = %v, want %v", input, got, want)
		}
	}
}

// noSystemd fails the test on any call: CLI tests must never touch the
// user's real services.
type noSystemd struct{ t *testing.T }

func (n noSystemd) DaemonReload() error             { n.t.Fatal("systemd used in a test"); return nil }
func (n noSystemd) EnableNow(units ...string) error { n.t.Fatal("systemd used in a test"); return nil }
func (n noSystemd) Reload(unit string) error        { n.t.Fatal("systemd used in a test"); return nil }
func (n noSystemd) Start(unit string) error         { n.t.Fatal("systemd used in a test"); return nil }
func (n noSystemd) Stop(units ...string) error      { n.t.Fatal("systemd used in a test"); return nil }
func (n noSystemd) Restart(unit string) error       { n.t.Fatal("systemd used in a test"); return nil }
func (n noSystemd) IsActive(unit string) string     { n.t.Fatal("systemd used in a test"); return "" }

func TestHumanSize(t *testing.T) {
	for n, want := range map[int64]string{0: "0 B", 512: "512 B", 2048: "2.0 KB", 5 << 20: "5.0 MB"} {
		if got := humanSize(n); got != want {
			t.Errorf("humanSize(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestRenderDoctor(t *testing.T) {
	var b strings.Builder
	renderDoctor(&b, []stack.Check{
		{Area: "system", Name: "CA", Status: stack.OK},
		{Area: "sites", Name: "foo", Status: stack.Fail, Detail: "no index.php", Fix: "rig unlink foo"},
	})
	out := b.String()
	for _, want := range []string{"FAIL", "foo", "fix: rig unlink foo", "system 1 ok, sites 0 ok"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "All good") {
		t.Error("says all good with a failure")
	}
}

func TestLogsNoFollow(t *testing.T) {
	if _, err := os.Stat("/usr/bin/tail"); err != nil {
		t.Skip("no /usr/bin/tail")
	}
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	logs := filepath.Join(state, "rig", "logs")
	os.MkdirAll(logs, 0o700)
	os.WriteFile(filepath.Join(logs, "nginx-error.log"), []byte("one\ntwo\nthree\n"), 0o600)

	var out, errOut bytes.Buffer
	if code := run([]string{"logs", "nginx", "-n", "2", "--no-follow"}, &out, &errOut); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	if out.String() != "two\nthree\n" {
		t.Errorf("output = %q", out.String())
	}
}
