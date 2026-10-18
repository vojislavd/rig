package db

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// testClient sets up rigadmin on a throwaway server and returns a Client
// using a login file pointed at that server's socket.
func testClient(t *testing.T) (*Client, *tempServer) {
	t.Helper()
	s := startTempServer(t)
	pw, _ := NewPassword()
	setup, err := AdminSetupSQL(pw)
	if err != nil {
		t.Fatal(err)
	}
	s.asSuper(t, string(setup))
	cnf := filepath.Join(s.dir, "rigadmin.cnf")
	body := fmt.Sprintf("[client]\nuser=%s\npassword=%s\nsocket=%s\n", AdminUser, pw, s.sock)
	if err := os.WriteFile(cnf, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 17, 19, 30, 0, 0, time.UTC)
	return &Client{Cnf: cnf, Backups: filepath.Join(s.dir, "backups"), Now: func() time.Time { return now }}, s
}

func TestClientLifecycle(t *testing.T) {
	c, s := testClient(t)

	name, pw, err := c.CreateSite("my-shop")
	if err != nil {
		t.Fatal(err)
	}
	if name != "rig_my_shop" || checkPassword(pw) != nil {
		t.Fatalf("CreateSite = %q, %q", name, pw)
	}
	if ok, err := c.Exists("my-shop"); err != nil || !ok {
		t.Fatalf("Exists = %v, %v", ok, err)
	}
	if list, _ := c.List(); len(list) != 1 || list[0] != "rig_my_shop" {
		t.Errorf("List = %v", list)
	}

	// The returned password works for the site user.
	if out, err := s.as(t, "rig_my_shop", pw, "CREATE TABLE rig_my_shop.orders (id INT); INSERT INTO rig_my_shop.orders VALUES (42);"); err != nil {
		t.Fatalf("site user: %v %s", err, out)
	}

	// Dump: private, gzipped, contains the data.
	path, err := c.Dump("my-shop")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(path) != "rig_my_shop-20261017-193000.sql.gz" {
		t.Errorf("dump name %s", filepath.Base(path))
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o600 {
		t.Errorf("dump mode %o, want 600", info.Mode().Perm())
	}
	dir, _ := os.Stat(c.Backups)
	if dir.Mode().Perm() != 0o700 {
		t.Errorf("backups folder mode %o, want 700", dir.Mode().Perm())
	}
	sql, err := readDump(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sql, "CREATE TABLE `orders`") || !strings.Contains(sql, "(42)") {
		t.Error("dump lacks the table or its data")
	}
	entries, _ := os.ReadDir(c.Backups)
	if len(entries) != 1 {
		t.Errorf("backups folder has %d entries, want 1 (no temp files)", len(entries))
	}

	// Calling CreateSite again resets the password; the old one stops working.
	_, pw2, err := c.CreateSite("my-shop")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.as(t, "rig_my_shop", pw, "SELECT 1;"); err == nil {
		t.Error("old password still works after reset")
	}
	if _, err := s.as(t, "rig_my_shop", pw2, "SELECT COUNT(*) FROM rig_my_shop.orders;"); err != nil {
		t.Error("new password does not work, or data was lost on reset")
	}

	// DropSite dumps first, then drops database and user.
	c.Now = func() time.Time { return time.Date(2026, 10, 17, 20, 0, 0, 0, time.UTC) }
	backup, err := c.DropSite("my-shop")
	if err != nil {
		t.Fatal(err)
	}
	if backup == "" {
		t.Fatal("DropSite made no backup")
	}
	if sql, _ := readDump(backup); !strings.Contains(sql, "(42)") {
		t.Error("pre-drop backup lacks the data")
	}
	if ok, _ := c.Exists("my-shop"); ok {
		t.Error("database still exists after DropSite")
	}
	if _, err := s.as(t, "rig_my_shop", pw2, "SELECT 1;"); err == nil {
		t.Error("site user still exists after DropSite")
	}

	// Dropping a site without a database just removes the (absent) user.
	if backup, err := c.DropSite("never-made"); err != nil || backup != "" {
		t.Errorf("DropSite(never-made) = %q, %v", backup, err)
	}
}

func TestClientRejectsBadNames(t *testing.T) {
	c := &Client{Cnf: "/nonexistent", Backups: t.TempDir(), Now: time.Now}
	for _, bad := range []string{"Foo", "../x", "a;b", ""} {
		if _, _, err := c.CreateSite(bad); err == nil {
			t.Errorf("CreateSite(%q) accepted", bad)
		}
		if _, err := c.DropSite(bad); err == nil {
			t.Errorf("DropSite(%q) accepted", bad)
		}
	}
}
