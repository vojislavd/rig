package stack

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/vojislavd/rig/internal/site"
)

func TestStartStopWorker(t *testing.T) {
	st, _ := linkedStack(t)
	sd := st.S.(*fakeSystemd)
	sd.calls = nil

	if err := st.StartWorker("foo", "queue"); err != nil {
		t.Fatal(err)
	}
	unit := filepath.Join(st.L.Units, "rig-foo-queue.service")
	data, err := os.ReadFile(unit)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "[Install]") {
		t.Error("worker unit can be enabled at login")
	}
	if !slices.Equal(sd.calls, []string{"daemon-reload", "start rig-foo-queue.service"}) {
		t.Errorf("calls = %v", sd.calls)
	}
	if slices.ContainsFunc(sd.calls, func(c string) bool { return strings.HasPrefix(c, "enable") }) {
		t.Error("worker was enabled")
	}

	// Starting again: same file, so no daemon-reload, just start.
	sd.calls = nil
	if err := st.StartWorker("foo", "queue"); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(sd.calls, []string{"start rig-foo-queue.service"}) {
		t.Errorf("second start calls = %v", sd.calls)
	}

	list, err := st.Workers()
	if err != nil || len(list) != 1 || list[0].Kind != "queue" {
		t.Errorf("Workers = %+v, %v", list, err)
	}

	sd.calls = nil
	if err := st.StopWorkers("foo", "queue"); err != nil {
		t.Fatal(err)
	}
	if fileExists(unit) {
		t.Error("unit file left after stop")
	}
	if !slices.Equal(sd.calls, []string{"stop rig-foo-queue.service", "daemon-reload"}) {
		t.Errorf("stop calls = %v", sd.calls)
	}
	if list, _ := st.Workers(); len(list) != 0 {
		t.Errorf("workers after stop = %+v", list)
	}
}

func TestStopAllOfSite(t *testing.T) {
	st, _ := linkedStack(t)
	st.StartWorker("foo", "queue")
	st.StartWorker("foo", "schedule")
	sd := st.S.(*fakeSystemd)
	sd.calls = nil
	if err := st.StopWorkers("foo", ""); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(sd.calls, "stop rig-foo-queue.service rig-foo-schedule.service") {
		t.Errorf("calls = %v", sd.calls)
	}
	if err := st.StopWorkers("foo", ""); err != nil {
		t.Errorf("stopping when nothing runs: %v", err)
	}
	if err := st.StopWorkers("foo", "queue"); err == nil {
		t.Error("stopping a named worker that isn't running should say so")
	}
}

func TestStopAllWorkers(t *testing.T) {
	st, _ := linkedStack(t)
	st.StartWorker("foo", "queue")
	if err := st.StopAllWorkers(); err != nil {
		t.Fatal(err)
	}
	if list, _ := st.Workers(); len(list) != 0 {
		t.Errorf("workers left: %+v", list)
	}
}

func TestWorkerRejects(t *testing.T) {
	st, _ := linkedStack(t)
	if err := st.StartWorker("foo", "tinker"); err == nil {
		t.Error("accepted an unknown kind")
	}
	if err := st.StartWorker("nope", "queue"); err == nil {
		t.Error("accepted an unknown site")
	}
	if err := st.RestartWorker("foo", "queue"); err == nil {
		t.Error("restarted a worker that isn't running")
	}
}

func TestUnlinkStopsWorkers(t *testing.T) {
	st, _ := linkedStack(t)
	st.StartWorker("foo", "queue")
	if err := st.Unlink("foo"); err != nil {
		t.Fatal(err)
	}
	if fileExists(filepath.Join(st.L.Units, "rig-foo-queue.service")) {
		t.Error("worker unit left after unlink")
	}
}

// withReverb makes the test project require laravel/reverb.
func withReverb(t *testing.T, dir string) {
	t.Helper()
	os.WriteFile(filepath.Join(dir, "composer.json"), []byte(`{"require":{"laravel/reverb":"^1.0"}}`), 0o600)
}

func TestStartReverb(t *testing.T) {
	st, dir := linkedStack(t)
	withReverb(t, dir)
	sd := st.S.(*fakeSystemd)
	sd.calls = nil
	if err := st.StartWorker("foo", "reverb"); err != nil {
		t.Fatal(err)
	}
	conf, _ := os.ReadFile(st.confPath("foo"))
	if !strings.Contains(string(conf), "proxy_pass http://127.0.0.1:6001;") {
		t.Error("site config has no Reverb route")
	}
	unit, _ := os.ReadFile(filepath.Join(st.L.Units, "rig-foo-reverb.service"))
	if !strings.Contains(string(unit), "reverb:start --host=127.0.0.1 --port=6001") {
		t.Errorf("reverb unit:\n%s", unit)
	}
	if !slices.Contains(sd.calls, "reload rig-nginx.service") || !slices.Contains(sd.calls, "start rig-foo-reverb.service") {
		t.Errorf("calls = %v", sd.calls)
	}

	// Second start: same port, no nginx reload.
	st.StopWorkers("foo", "reverb")
	sd.calls = nil
	if err := st.StartWorker("foo", "reverb"); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(sd.calls, "reload rig-nginx.service") {
		t.Errorf("nginx reloaded although nothing changed: %v", sd.calls)
	}

	// rig env now also sets the Reverb keys.
	os.WriteFile(filepath.Join(dir, ".env"), []byte("REVERB_APP_ID=123456\nREVERB_APP_KEY=\nREVERB_HOST=127.0.0.1\nREVERB_PORT=8080\nREVERB_SCHEME=http\n"), 0o600)
	if _, _, err := st.Env("foo", nil, func(EnvPreview) bool { return true }); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, ".env"))
	for _, want := range []string{"REVERB_APP_ID=123456\n", "REVERB_HOST=foo.localhost\n", "REVERB_PORT=443\n", "REVERB_SCHEME=https\n", "REVERB_SERVER_HOST=127.0.0.1\n", "REVERB_SERVER_PORT=6001\n"} {
		if !strings.Contains(string(data), want) {
			t.Errorf(".env missing %q:\n%s", want, data)
		}
	}
	// The empty key was filled with 20 random characters; the secret added.
	if !regexp.MustCompile(`(?m)^REVERB_APP_KEY=[a-z0-9]{20}$`).Match(data) || !regexp.MustCompile(`(?m)^REVERB_APP_SECRET=[a-z0-9]{20}$`).Match(data) {
		t.Errorf("credentials not generated:\n%s", data)
	}
}

func TestStartReverbUpdatesMainConfig(t *testing.T) {
	st, dir := linkedStack(t)
	withReverb(t, dir)
	// Simulate an nginx.conf from before the websocket map existed.
	mainConf := filepath.Join(st.L.Config, "nginx", "nginx.conf")
	os.WriteFile(mainConf, []byte("# old\n"), 0o600)
	sd := st.S.(*fakeSystemd)
	sd.calls = nil
	if err := st.StartWorker("foo", "reverb"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(mainConf)
	if !strings.Contains(string(data), "map $http_upgrade $connection_upgrade") {
		t.Error("nginx.conf not refreshed with the websocket map")
	}
	if !slices.Contains(sd.calls, "reload rig-nginx.service") {
		t.Error("nginx not reloaded")
	}
}

func TestReverbRefusedWithoutPackage(t *testing.T) {
	st, _ := linkedStack(t)
	if err := st.StartWorker("foo", "reverb"); err == nil || !strings.Contains(err.Error(), "laravel/reverb") {
		t.Fatalf("StartWorker = %v, want refusal", err)
	}
	conf, _ := os.ReadFile(st.confPath("foo"))
	if strings.Contains(string(conf), "proxy_pass") {
		t.Error("route added although Reverb was refused")
	}
	r, _ := site.Load(st.L.Registry())
	if s, _ := r.Get("foo"); s.ReverbPort != 0 {
		t.Error("port assigned although Reverb was refused")
	}
}

func TestRemoveReverb(t *testing.T) {
	st, dir := linkedStack(t)
	withReverb(t, dir)
	if err := st.StartWorker("foo", "reverb"); err != nil {
		t.Fatal(err)
	}
	if err := st.RemoveReverb("foo"); err != nil {
		t.Fatal(err)
	}
	conf, _ := os.ReadFile(st.confPath("foo"))
	if strings.Contains(string(conf), "proxy_pass") {
		t.Error("Reverb route left in the site config")
	}
	r, _ := site.Load(st.L.Registry())
	if s, _ := r.Get("foo"); s.ReverbPort != 0 {
		t.Error("Reverb port not released")
	}
	if fileExists(filepath.Join(st.L.Units, "rig-foo-reverb.service")) {
		t.Error("worker unit left")
	}
	if err := st.RemoveReverb("foo"); err != nil {
		t.Errorf("second RemoveReverb: %v", err)
	}
}
