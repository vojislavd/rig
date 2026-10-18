package stack

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeDB struct {
	calls int
	pw    string
}

func (f *fakeDB) CreateSite(name string) (string, string, error) {
	f.calls++
	return "rig_" + strings.ReplaceAll(name, "-", "_"), f.pw, nil
}

const examplePW = "RealPasswordRealPasswordRealPassword1234"

func linkedStack(t *testing.T) (*Stack, string) {
	t.Helper()
	st, _ := testStack(t)
	dir := filepath.Join(st.L.Code, "foo")
	if _, err := st.Link(dir, ""); err != nil {
		t.Fatal(err)
	}
	return st, dir
}

func TestEnvWritesDotEnv(t *testing.T) {
	st, dir := linkedStack(t)
	envPath := filepath.Join(dir, ".env")
	os.WriteFile(envPath, []byte("APP_KEY=base64:k=\nDB_CONNECTION=sqlite\n# DB_HOST=127.0.0.1\n"), 0o644)

	dbc := &fakeDB{pw: examplePW}
	var seen EnvPreview
	preview, applied, err := st.Env("foo", dbc, func(p EnvPreview) bool { seen = p; return true })
	if err != nil || !applied {
		t.Fatalf("Env = %v, %v", applied, err)
	}
	if preview.OldMode != "0644" {
		t.Errorf("OldMode = %q, want 0644", preview.OldMode)
	}
	for _, c := range seen.Changes {
		if strings.Contains(c.Old+c.New, examplePW) || strings.Contains(c.Old+c.New, "xxxx") {
			t.Errorf("preview shows a password: %+v", c)
		}
	}
	data, _ := os.ReadFile(envPath)
	s := string(data)
	for _, want := range []string{"APP_KEY=base64:k=\n", "APP_URL=https://foo.localhost\n", "DB_CONNECTION=mariadb\n", "DB_HOST=127.0.0.1\n", "DB_DATABASE=rig_foo\n", "DB_PASSWORD=" + examplePW + "\n"} {
		if !strings.Contains(s, want) {
			t.Errorf(".env missing %q:\n%s", want, s)
		}
	}
	info, _ := os.Stat(envPath)
	if info.Mode().Perm() != 0o600 {
		t.Errorf(".env mode %o, want 600", info.Mode().Perm())
	}
}

func TestEnvDeclinedChangesNothing(t *testing.T) {
	st, dir := linkedStack(t)
	envPath := filepath.Join(dir, ".env")
	original := []byte("DB_CONNECTION=sqlite\n")
	os.WriteFile(envPath, original, 0o644)
	dbc := &fakeDB{pw: examplePW}

	_, applied, err := st.Env("foo", dbc, func(EnvPreview) bool { return false })
	if err != nil || applied {
		t.Fatalf("Env = %v, %v", applied, err)
	}
	if dbc.calls != 0 {
		t.Error("database touched although the user declined")
	}
	if data, _ := os.ReadFile(envPath); string(data) != string(original) {
		t.Error(".env changed although the user declined")
	}
}

func TestEnvStartsFromExample(t *testing.T) {
	st, dir := linkedStack(t)
	os.WriteFile(filepath.Join(dir, ".env.example"), []byte("APP_NAME=Shop\n# DB_HOST=127.0.0.1\n"), 0o644)
	preview, applied, err := st.Env("foo", &fakeDB{pw: examplePW}, func(EnvPreview) bool { return true })
	if err != nil || !applied || !preview.FromExample {
		t.Fatalf("Env = %+v, %v, %v", preview, applied, err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, ".env"))
	if !strings.HasPrefix(string(data), "APP_NAME=Shop\nDB_HOST=127.0.0.1\n") {
		t.Errorf(".env = %q", data)
	}
	if example, _ := os.ReadFile(filepath.Join(dir, ".env.example")); strings.Contains(string(example), examplePW) {
		t.Error(".env.example was modified")
	}
}

func TestEnvRefusesSymlink(t *testing.T) {
	st, dir := linkedStack(t)
	target := filepath.Join(t.TempDir(), "elsewhere")
	os.WriteFile(target, []byte("X=1\n"), 0o600)
	os.Symlink(target, filepath.Join(dir, ".env"))
	dbc := &fakeDB{pw: examplePW}
	if _, _, err := st.Env("foo", dbc, func(EnvPreview) bool { return true }); err == nil {
		t.Fatal("Env wrote through a symlinked .env")
	}
	if dbc.calls != 0 {
		t.Error("database touched for a refused .env")
	}
}

func TestEnvUnknownSite(t *testing.T) {
	st, _ := testStack(t)
	if _, _, err := st.Env("nope", &fakeDB{pw: examplePW}, func(EnvPreview) bool { return true }); err == nil {
		t.Fatal("Env accepted an unknown site")
	}
}

func TestEnvWithoutDatabase(t *testing.T) {
	st, dir := linkedStack(t)
	envPath := filepath.Join(dir, ".env")
	os.WriteFile(envPath, []byte("APP_URL=http://localhost\nDB_CONNECTION=sqlite\n"), 0o644)
	preview, applied, err := st.Env("foo", nil, func(EnvPreview) bool { return true })
	if err != nil || !applied {
		t.Fatalf("Env = %v, %v", applied, err)
	}
	for _, c := range preview.Changes {
		if strings.HasPrefix(c.Key, "DB_") {
			t.Errorf("--no-db changed %s", c.Key)
		}
	}
	data, _ := os.ReadFile(envPath)
	if !strings.HasPrefix(string(data), "APP_URL=https://foo.localhost\nDB_CONNECTION=sqlite\n") || !strings.Contains(string(data), "MAIL_HOST=127.0.0.1\n") {
		t.Errorf(".env = %q", data)
	}
}

func TestEnvSetsMailpit(t *testing.T) {
	st, dir := linkedStack(t)
	envPath := filepath.Join(dir, ".env")
	os.WriteFile(envPath, []byte("MAIL_MAILER=log\nMAIL_PASSWORD=hunter2hunter2\n"), 0o600)
	var seen EnvPreview
	if _, _, err := st.Env("foo", nil, func(p EnvPreview) bool { seen = p; return true }); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(envPath)
	for _, want := range []string{"MAIL_MAILER=smtp\n", "MAIL_HOST=127.0.0.1\n", "MAIL_PORT=1025\n", "MAIL_PASSWORD=null\n", "MAIL_SCHEME=null\n"} {
		if !strings.Contains(string(data), want) {
			t.Errorf(".env missing %q", want)
		}
	}
	for _, c := range seen.Changes {
		if strings.Contains(c.Old+c.New, "hunter2") {
			t.Errorf("preview shows the old SMTP password: %+v", c)
		}
	}
}
