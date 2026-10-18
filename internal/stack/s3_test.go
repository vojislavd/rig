package stack

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// fakeGarage records admin calls.
type fakeGarage struct{ calls []string }

func (f *fakeGarage) WaitReady(time.Duration) error { f.calls = append(f.calls, "wait"); return nil }
func (f *fakeGarage) InitLayout() error             { f.calls = append(f.calls, "layout"); return nil }
func (f *fakeGarage) Bucket(alias string) (string, error) {
	f.calls = append(f.calls, "bucket "+alias)
	return "b-" + alias, nil
}
func (f *fakeGarage) Key(name string) (string, string, error) {
	f.calls = append(f.calls, "key "+name)
	return "GKtest" + name, "secret-of-" + name, nil
}
func (f *fakeGarage) Allow(b, k string) error {
	f.calls = append(f.calls, "allow "+b+" "+k)
	return nil
}

func withFakeGarage(t *testing.T) *fakeGarage {
	t.Helper()
	fg := &fakeGarage{}
	old := newGarageAdmin
	newGarageAdmin = func(string) garageAdmin { return fg }
	t.Cleanup(func() { newGarageAdmin = old })
	return fg
}

func TestStartStopS3(t *testing.T) {
	st, _ := testStack(t)
	fg := withFakeGarage(t)
	sd := st.S.(*fakeSystemd)

	if err := st.StartS3(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(st.garageConf())
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("garage.toml: %v, mode %o", err, info.Mode().Perm())
	}
	sec1, err := st.garageSecrets()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(sd.calls, "start rig-garage.service") || !slices.Equal(fg.calls, []string{"wait", "layout"}) {
		t.Errorf("systemd %v, garage %v", sd.calls, fg.calls)
	}

	// Starting again keeps the same secrets.
	if err := st.StartS3(); err != nil {
		t.Fatal(err)
	}
	if sec2, _ := st.garageSecrets(); sec2 != sec1 {
		t.Error("secrets changed on the second start")
	}

	if err := st.StopS3(); err != nil {
		t.Fatal(err)
	}
	if st.S3State() != "stopped" {
		t.Error("still running after stop")
	}
	if _, err := os.Stat(st.garageConf()); err != nil {
		t.Error("garage.toml removed on stop; it holds the secrets and must stay")
	}
}

func TestStartS3RefusesLooseConfig(t *testing.T) {
	st, _ := testStack(t)
	withFakeGarage(t)
	if err := st.StartS3(); err != nil {
		t.Fatal(err)
	}
	os.Chmod(st.garageConf(), 0o644)
	if err := st.StartS3(); err == nil {
		t.Error("used a world-readable garage.toml")
	}
}

func TestEnableS3(t *testing.T) {
	st, dir := linkedStack(t)
	fg := withFakeGarage(t)
	sd := st.S.(*fakeSystemd)

	if err := st.EnableS3("foo"); err == nil {
		t.Fatal("EnableS3 worked while Garage was stopped")
	}
	if err := st.StartS3(); err != nil {
		t.Fatal(err)
	}
	fg.calls, sd.calls = nil, nil
	if err := st.EnableS3("foo"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"bucket rig-foo", "key rig-foo", "allow b-rig-foo GKtestrig-foo"} {
		if !slices.Contains(fg.calls, want) {
			t.Errorf("garage calls %v lack %q", fg.calls, want)
		}
	}
	conf, _ := os.ReadFile(st.confPath("foo"))
	if !strings.Contains(string(conf), "server_name files.foo.localhost;") {
		t.Error("no files server in the site config")
	}
	if !slices.Contains(sd.calls, "reload rig-nginx.service") {
		t.Error("nginx not reloaded")
	}

	os.WriteFile(filepath.Join(dir, ".env"), []byte("FILESYSTEM_DISK=local\nAWS_ACCESS_KEY_ID=\n"), 0o600)
	var seen EnvPreview
	if _, _, err := st.Env("foo", nil, func(p EnvPreview) bool { seen = p; return true }); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, ".env"))
	for _, want := range []string{
		"FILESYSTEM_DISK=s3\n", "AWS_ACCESS_KEY_ID=GKtestrig-foo\n", "AWS_SECRET_ACCESS_KEY=secret-of-rig-foo\n",
		"AWS_BUCKET=rig-foo\n", "AWS_ENDPOINT=https://files.foo.localhost\n", "AWS_USE_PATH_STYLE_ENDPOINT=true\n", "AWS_DEFAULT_REGION=garage\n",
	} {
		if !strings.Contains(string(data), want) {
			t.Errorf(".env missing %q", want)
		}
	}
	for _, c := range seen.Changes {
		if strings.Contains(c.Old+c.New, "secret-of") {
			t.Errorf("preview shows the S3 secret: %+v", c)
		}
	}

	// With Garage stopped, rig env refuses rather than writing half the config.
	st.StopS3()
	if _, _, err := st.Env("foo", nil, func(EnvPreview) bool { return true }); err == nil || !strings.Contains(err.Error(), "rig s3 start") {
		t.Errorf("Env with Garage stopped = %v", err)
	}
}
