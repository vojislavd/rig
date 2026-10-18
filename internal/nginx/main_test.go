package nginx

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func testDirs(root string) Dirs {
	return Dirs{
		Conf:    filepath.Join(root, "conf"),
		Logs:    filepath.Join(root, "logs"),
		Runtime: filepath.Join(root, "run"),
	}
}

func TestMainConfigContent(t *testing.T) {
	conf, err := MainConfig(testDirs("/home/u/x"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(conf)
	for _, want := range []string{
		"listen 127.0.0.1:8080 default_server;",
		"return 444;",
		"listen 127.0.0.1:8443 ssl default_server;",
		"ssl_reject_handshake on;",
		"server_tokens off;",
		"include /home/u/x/conf/rig.d/*.conf;",
		"pid /home/u/x/run/nginx.pid;",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("config missing %q", want)
		}
	}
	// Never run as another user, never listen beyond loopback.
	for _, banned := range []string{"\nuser ", "0.0.0.0", "[::", "listen 80", "listen 443"} {
		if strings.Contains(s, banned) {
			t.Errorf("config contains %q", banned)
		}
	}
	// Every listen line is on 127.0.0.1.
	for _, line := range strings.Split(s, "\n") {
		if l := strings.TrimSpace(line); strings.HasPrefix(l, "listen ") && !strings.HasPrefix(l, "listen 127.0.0.1:") {
			t.Errorf("listen not on loopback: %q", l)
		}
	}
}

func TestMainConfigRejectsUnsafePaths(t *testing.T) {
	for _, bad := range []string{
		"relative/conf",
		"/home/u/my conf",       // space
		"/home/u/x;evil",        // ends the directive
		"/home/u/x{",            // opens a block
		"/home/u/$var",          // nginx variable
		"/home/u/x#comment",     // comment
		"/home/u/'q'",           // quote
		"/home/u/../etc",        // not clean
		"/home/u/x\nuser root;", // newline injection
	} {
		d := testDirs("/home/u/ok")
		d.Conf = bad
		if _, err := MainConfig(d); err == nil {
			t.Errorf("MainConfig accepted conf dir %q", bad)
		}
	}
}

// TestMainConfigNginxAccepts runs the real "nginx -t" on the generated
// config, if nginx is installed. It only reads the config; nothing is
// started and no port is opened.
func TestMainConfigNginxAccepts(t *testing.T) {
	nginx, err := exec.LookPath("nginx")
	if err != nil {
		t.Skip("nginx not installed")
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	d := testDirs(root)
	for _, dir := range []string{filepath.Join(d.Conf, "rig.d"), d.Logs, d.Runtime} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	conf, err := MainConfig(d)
	if err != nil {
		t.Fatal(err)
	}
	confPath := filepath.Join(d.Conf, "nginx.conf")
	if err := os.WriteFile(confPath, conf, 0o600); err != nil {
		t.Fatal(err)
	}

	// -e: log startup errors here instead of root-owned /var/log/nginx.
	out, err := exec.Command(nginx, "-t", "-q", "-e", filepath.Join(d.Logs, "startup.log"), "-c", confPath).CombinedOutput()
	if err != nil {
		t.Fatalf("nginx -t failed: %v\n%s", err, out)
	}
}
