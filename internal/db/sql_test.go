package db

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestName(t *testing.T) {
	for in, want := range map[string]string{"foo": "rig_foo", "my-shop": "rig_my_shop", "a-b-c": "rig_a_b_c"} {
		if got, err := Name(in); err != nil || got != want {
			t.Errorf("Name(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "Foo", "foo_bar", "rig", "../x"} {
		if _, err := Name(bad); err == nil {
			t.Errorf("Name(%q) accepted", bad)
		}
	}
}

func TestNewPassword(t *testing.T) {
	a, err := NewPassword()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := NewPassword()
	if a == b || checkPassword(a) != nil || len(a) != passwordLen {
		t.Errorf("bad passwords %q %q", a, b)
	}
}

func TestSetupSQLRejectsBadPassword(t *testing.T) {
	for _, bad := range []string{"short", "has'quote" + strings.Repeat("a", 32), strings.Repeat("a", 31)} {
		if _, err := AdminSetupSQL(bad); err == nil {
			t.Errorf("AdminSetupSQL accepted %q", bad)
		}
	}
}

func TestRedact(t *testing.T) {
	got := string(Redact([]byte("IDENTIFIED BY 'secret123'"), "secret123"))
	if strings.Contains(got, "secret123") {
		t.Errorf("Redact left the password: %s", got)
	}
}

// tempServer is a private MariaDB in a temp folder: no network, its own
// socket, removed after the test. Nothing touches the real server.
type tempServer struct {
	dir, sock string
}

func startTempServer(t *testing.T) *tempServer {
	t.Helper()
	for _, bin := range []string{"mariadbd", "mariadb-install-db", "mariadb"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skip(bin + " not installed")
		}
	}
	if testing.Short() {
		t.Skip("starts a MariaDB server")
	}
	dir, err := os.MkdirTemp("", "rigdb")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	s := &tempServer{dir: dir, sock: filepath.Join(dir, "s")}
	data := filepath.Join(dir, "data")

	out, err := exec.Command("mariadb-install-db", "--no-defaults", "--datadir="+data,
		"--auth-root-authentication-method=socket", "--skip-test-db").CombinedOutput()
	if err != nil {
		t.Fatalf("install-db: %v\n%s", err, out)
	}
	srv := exec.Command("mariadbd", "--no-defaults", "--datadir="+data, "--socket="+s.sock,
		"--skip-networking", "--pid-file="+filepath.Join(dir, "pid"), "--log-error="+filepath.Join(dir, "err"))
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Process.Kill(); srv.Wait() })
	for i := 0; i < 100; i++ {
		if _, err := os.Stat(s.sock); err == nil {
			return s
		}
		time.Sleep(100 * time.Millisecond)
	}
	log, _ := os.ReadFile(filepath.Join(dir, "err"))
	t.Fatalf("server did not start:\n%s", log)
	return nil
}

// asSuper runs SQL as the OS user's socket account (all privileges, like
// root on a real server).
func (s *tempServer) asSuper(t *testing.T, sql string) {
	t.Helper()
	u, _ := user.Current()
	cmd := exec.Command("mariadb", "--no-defaults", "--socket="+s.sock, "-u", u.Username)
	cmd.Stdin = strings.NewReader(sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("super SQL failed: %v\n%s", err, out)
	}
}

// as runs SQL as user/password through a 0600 defaults file (the way rig
// passes passwords), returning output and error.
func (s *tempServer) as(t *testing.T, user, password, sql string) (string, error) {
	t.Helper()
	cnf := filepath.Join(s.dir, user+".cnf")
	body := fmt.Sprintf("[client]\nuser=%s\npassword=%s\nsocket=%s\n", user, password, s.sock)
	if err := os.WriteFile(cnf, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("mariadb", "--defaults-extra-file="+cnf, "--batch", "--skip-column-names")
	cmd.Stdin = strings.NewReader(sql)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	return out.String(), err
}

func TestAdminSQLOnTempServer(t *testing.T) {
	s := startTempServer(t)
	adminPW, _ := NewPassword()
	setup, err := AdminSetupSQL(adminPW)
	if err != nil {
		t.Fatal(err)
	}
	// The temp server's super account plays root; the procedures name
	// root@localhost as definer, which mariadb-install-db created.
	s.asSuper(t, string(setup))
	s.asSuper(t, string(setup)) // running it twice must work

	if strings.Contains(string(setup), adminPW) {
		t.Fatal("setup SQL contains the plaintext admin password")
	}

	sitePW, _ := NewPassword()
	siteHash, _ := Hash(sitePW)
	if out, err := s.as(t, AdminUser, adminPW, "CALL rigsys.create_site('rig_foo', '"+siteHash+"');"); err != nil {
		t.Fatalf("create_site: %v\n%s", err, out)
	}
	if out, err := s.as(t, AdminUser, adminPW, "CALL rigsys.create_site('rig_bar', '"+siteHash+"');"); err != nil {
		t.Fatalf("create_site bar: %v\n%s", err, out)
	}

	// The site user works on its own database only.
	if out, err := s.as(t, "rig_foo", sitePW, "CREATE TABLE rig_foo.t (id INT); INSERT INTO rig_foo.t VALUES (1); SELECT COUNT(*) FROM rig_foo.t;"); err != nil || strings.TrimSpace(out) != "1" {
		t.Fatalf("site user on own db: %v %q", err, out)
	}
	mustFail := func(who, pw, sql, why string) {
		t.Helper()
		out, err := s.as(t, who, pw, sql)
		if err == nil {
			t.Errorf("%s: succeeded as %s, want failure\n%s", why, who, out)
			return
		}
		// Fail for the right reason: a permission or validation error,
		// not e.g. a connection problem.
		if !strings.Contains(out, "denied") && !strings.Contains(out, "rig: invalid") {
			t.Errorf("%s: failed for an unexpected reason: %s", why, out)
		}
	}
	mustFail("rig_foo", sitePW, "SELECT 1 FROM rig_bar.t;", "site user reads another site")
	mustFail("rig_foo", sitePW, "DROP DATABASE rig_bar;", "site user drops another site")

	// rigadmin can't escape the rig_ databases.
	mustFail(AdminUser, adminPW, "ALTER USER 'root'@'localhost' IDENTIFIED BY 'x';", "admin changes root's password")
	mustFail(AdminUser, adminPW, "CREATE USER 'evil'@'localhost';", "admin creates a user")
	mustFail(AdminUser, adminPW, "SELECT * FROM mysql.user;", "admin reads mysql.user")
	mustFail(AdminUser, adminPW, "CREATE DATABASE other;", "admin creates a non-rig database")
	mustFail(AdminUser, adminPW, "GRANT ALL ON *.* TO 'rig_foo'@'localhost';", "admin grants global rights")
	mustFail(AdminUser, adminPW, "DROP PROCEDURE rigsys.create_site;", "admin replaces the procedures")

	// The procedures refuse anything but rig_<name> and plain passwords.
	for _, bad := range []string{
		"mysql", "RIG_FOO", "rig_", "rig_foo; DROP DATABASE mysql", "rig_foo`", "rig_foo'@'%", "rig__x", "rigadmin",
	} {
		mustFail(AdminUser, adminPW, fmt.Sprintf("CALL rigsys.create_site('%s', '%s');", strings.ReplaceAll(bad, "'", "''"), siteHash), "create_site("+bad+")")
		mustFail(AdminUser, adminPW, fmt.Sprintf("CALL rigsys.drop_site('%s');", strings.ReplaceAll(bad, "'", "''")), "drop_site("+bad+")")
	}
	mustFail(AdminUser, adminPW, "CALL rigsys.create_site('rig_baz', '"+sitePW+"');", "plaintext instead of a hash")
	mustFail(AdminUser, adminPW, "CALL rigsys.create_site('rig_baz', '*"+strings.Repeat("A", 38)+"''x');", "hash with a quote")
	mustFail(AdminUser, adminPW, "CALL rigsys.create_site('rig_baz', '"+strings.ToLower(siteHash)+"');", "lowercase hash")

	// drop_site removes database and user.
	if out, err := s.as(t, AdminUser, adminPW, "CALL rigsys.drop_site('rig_bar');"); err != nil {
		t.Fatalf("drop_site: %v\n%s", err, out)
	}
	mustFail("rig_bar", sitePW, "SELECT 1;", "dropped site user logs in")

	// Removal leaves site databases alone.
	s.asSuper(t, string(AdminRemoveSQL()))
	mustFail(AdminUser, adminPW, "SELECT 1;", "rigadmin after removal")
	if out, err := s.as(t, "rig_foo", sitePW, "SELECT COUNT(*) FROM rig_foo.t;"); err != nil || strings.TrimSpace(out) != "1" {
		t.Errorf("site data gone after admin removal: %v %q", err, out)
	}
}

func TestHash(t *testing.T) {
	got, err := Hash("abcdefghijklmnopqrstuvwxyz012345")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 41 || got[0] != '*' || strings.ToUpper(got) != got {
		t.Errorf("Hash = %q, want * + 40 uppercase hex", got)
	}
	if _, err := Hash("short"); err == nil {
		t.Error("Hash accepted an invalid password")
	}
}
