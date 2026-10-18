package stack

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

type fakeDropper struct {
	exists  bool
	failErr error // Dump fails with this
	calls   []string
}

func (f *fakeDropper) Exists(string) (bool, error) { return f.exists, nil }

func (f *fakeDropper) Dump(name string) (string, error) {
	f.calls = append(f.calls, "dump "+name)
	if f.failErr != nil {
		return "", f.failErr
	}
	return "/backups/rig_" + name + ".sql.gz", nil
}

func (f *fakeDropper) Drop(name string) error {
	f.calls = append(f.calls, "drop "+name)
	return nil
}

func TestRemoveKeepsCode(t *testing.T) {
	st, dir := linkedStack(t)
	d := &fakeDropper{exists: true}
	p, err := st.PlanRemove("foo", d, false)
	if err != nil {
		t.Fatal(err)
	}
	backup, err := st.Remove(p, d)
	if err != nil {
		t.Fatal(err)
	}
	if backup == "" || !slices.Equal(d.calls, []string{"dump foo", "drop foo"}) {
		t.Errorf("backup %q, calls %v", backup, d.calls)
	}
	if fileExists(st.confPath("foo")) {
		t.Error("site still linked")
	}
	if !fileExists(dir) {
		t.Error("code deleted without --purge")
	}
}

func TestRemovePurge(t *testing.T) {
	st, dir := linkedStack(t)
	d := &fakeDropper{}
	p, err := st.PlanRemove("foo", d, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Remove(p, d); err != nil {
		t.Fatal(err)
	}
	if fileExists(dir) {
		t.Error("project folder still exists after purge")
	}
	if !fileExists(st.L.Code) {
		t.Fatal("purge deleted the code folder itself")
	}
}

func TestRemoveStopsWhenBackupFails(t *testing.T) {
	st, dir := linkedStack(t)
	d := &fakeDropper{exists: true, failErr: errors.New("disk full")}
	p, _ := st.PlanRemove("foo", d, true)
	if _, err := st.Remove(p, d); err == nil {
		t.Fatal("Remove went on after a failed backup")
	}
	if slices.Contains(d.calls, "drop foo") {
		t.Error("database dropped although the backup failed")
	}
	if !fileExists(st.confPath("foo")) || !fileExists(dir) {
		t.Error("site unlinked or code deleted although the backup failed")
	}
}

func TestPurgeRefusesSwappedFolder(t *testing.T) {
	st, dir := linkedStack(t)
	d := &fakeDropper{}
	p, _ := st.PlanRemove("foo", d, true)

	// Between preview and removal, the folder is replaced by a symlink to
	// somewhere precious.
	precious := t.TempDir()
	os.WriteFile(filepath.Join(precious, "keep"), []byte("x"), 0o600)
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(precious, dir); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Remove(p, d); err == nil || !strings.Contains(err.Error(), "refusing to delete") {
		t.Fatalf("Remove = %v, want refusal", err)
	}
	if !fileExists(filepath.Join(precious, "keep")) {
		t.Fatal("purge followed the symlink and deleted files")
	}
}

func TestPlanRemoveUnknown(t *testing.T) {
	st, _ := testStack(t)
	if _, err := st.PlanRemove("nope", &fakeDropper{}, false); err == nil {
		t.Error("PlanRemove accepted an unknown site")
	}
}

func TestGitWarnings(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	if w := gitWarnings(dir); len(w) != 1 || !strings.Contains(w[0], "not a git repository") {
		t.Errorf("no repo: %v", w)
	}
	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	run("init", "-q")
	os.WriteFile(filepath.Join(dir, "a"), []byte("x"), 0o600)
	run("add", "a")
	run("commit", "-qm", "c")
	os.WriteFile(filepath.Join(dir, "b"), []byte("y"), 0o600)
	w := strings.Join(gitWarnings(dir), "; ")
	if !strings.Contains(w, "1 uncommitted change") || !strings.Contains(w, "no upstream") {
		t.Errorf("warnings = %q", w)
	}
}

func TestRemoveUnlinksBeforeDrop(t *testing.T) {
	st, _ := linkedStack(t)
	d := &fakeDropper{exists: true}
	linkedAtDrop := true
	// "Served" while the site config exists, like a real nginx.
	st.Served = func(string) bool { return fileExists(st.confPath("foo")) }
	p, _ := st.PlanRemove("foo", d, false)
	if _, err := st.Remove(p, &orderCheck{fakeDropper: d, st: st, linked: &linkedAtDrop}); err != nil {
		t.Fatal(err)
	}
	if linkedAtDrop {
		t.Error("database dropped while the site was still served")
	}
}

// orderCheck notes, at Drop time, whether the site config still exists.
type orderCheck struct {
	*fakeDropper
	st     *Stack
	linked *bool
}

func (o *orderCheck) Drop(name string) error {
	*o.linked = fileExists(o.st.confPath(name))
	return o.fakeDropper.Drop(name)
}

func TestWaitServed(t *testing.T) {
	st, _ := testStack(t)
	st.Served = func(string) bool { return false }
	if err := st.waitServed("x.localhost", false); err != nil {
		t.Errorf("want nil, got %v", err)
	}
	if err := st.waitServed("x.localhost", true); err == nil {
		t.Error("waitServed(true) succeeded although never served")
	}
}

func TestLinkRollsBackWhenNotServed(t *testing.T) {
	st, _ := testStack(t)
	st.Served = func(string) bool { return false } // nginx never picks it up
	if _, err := st.Link(filepath.Join(st.L.Code, "foo"), ""); err == nil {
		t.Fatal("Link succeeded although nginx never served the site")
	}
	if fileExists(st.confPath("foo")) {
		t.Error("site config left behind")
	}
}
