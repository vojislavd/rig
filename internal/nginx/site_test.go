package nginx

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vojislavd/rig/internal/ca"
)

func testSite() Site {
	return Site{
		Name:   "foo",
		Dir:    "/home/u/code/foo",
		Certs:  "/home/u/.local/share/rig/certs",
		Socket: "/run/user/1000/rig/php85/fpm.sock",
	}
}

func TestSiteConfigContent(t *testing.T) {
	conf, err := SiteConfig(testSite())
	if err != nil {
		t.Fatal(err)
	}
	s := string(conf)
	for _, want := range []string{
		"listen 127.0.0.1:8080;",
		"return 301 https://foo.localhost$request_uri;",
		"listen 127.0.0.1:8443 ssl;",
		"server_name foo.localhost;",
		"ssl_certificate     /home/u/.local/share/rig/certs/foo.crt;",
		"ssl_certificate_key /home/u/.local/share/rig/certs/foo.key;",
		"root /home/u/code/foo/public;",
		"location ~ /\\.(?!well-known) {\n        deny all;",
		"location ~ ^/index\\.php(/|$) {",
		"fastcgi_pass unix:/run/user/1000/rig/php85/fpm.sock;",
		"fastcgi_hide_header X-Powered-By;",
		"location ~ \\.php$ {\n        return 404;",
		`add_header X-Content-Type-Options "nosniff" always;`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("site config missing %q", want)
		}
	}
	for _, banned := range []string{"$host", "Strict-Transport-Security", "autoindex on", "0.0.0.0", "[::"} {
		if strings.Contains(s, banned) {
			t.Errorf("site config contains %q", banned)
		}
	}
	// Exactly one place passes requests to PHP.
	if n := strings.Count(s, "fastcgi_pass"); n != 1 {
		t.Errorf("found %d fastcgi_pass, want 1", n)
	}
}

func TestSiteConfigRejects(t *testing.T) {
	bad := []func(*Site){
		func(s *Site) { s.Name = "Foo" },
		func(s *Site) { s.Name = "foo;evil" },
		func(s *Site) { s.Name = "rig" },
		func(s *Site) { s.Dir = "/home/u/code/my site" },
		func(s *Site) { s.Dir = "/home/u/code/foo;\n}" },
		func(s *Site) { s.Socket = "relative.sock" },
		func(s *Site) { s.Certs = "/home/u/$certs" },
	}
	for i, change := range bad {
		s := testSite()
		change(&s)
		if _, err := SiteConfig(s); err == nil {
			t.Errorf("case %d: SiteConfig accepted %+v", i, s)
		}
	}
}

// TestSiteConfigNginxAccepts runs the real "nginx -t" on the main config
// plus a site config with real certificates from a temporary CA.
func TestSiteConfigNginxAccepts(t *testing.T) {
	bin, err := exec.LookPath("nginx")
	if err != nil {
		t.Skip("nginx not installed")
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	d := Dirs{Conf: filepath.Join(root, "conf"), Logs: filepath.Join(root, "logs"), Runtime: filepath.Join(root, "run")}
	certs := filepath.Join(root, "certs")
	siteDir := filepath.Join(root, "code", "foo")
	for _, dir := range []string{filepath.Join(d.Conf, "rig.d"), d.Logs, d.Runtime, filepath.Join(siteDir, "public")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}

	c, err := ca.New(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := c.IssueLeaf("foo", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := leaf.Save(certs, "foo"); err != nil {
		t.Fatal(err)
	}

	mainConf, err := MainConfig(d)
	if err != nil {
		t.Fatal(err)
	}
	siteConf, err := SiteConfig(Site{Name: "foo", Dir: siteDir, Certs: certs, Socket: filepath.Join(d.Runtime, "fpm.sock"), ReverbPort: 6001, S3: true, S3Addr: "127.0.0.1:3900"})
	if err != nil {
		t.Fatal(err)
	}
	confPath := filepath.Join(d.Conf, "nginx.conf")
	if err := os.WriteFile(confPath, mainConf, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(SiteConfigPath(d.Conf, "foo"), siteConf, 0o600); err != nil {
		t.Fatal(err)
	}

	out, err := exec.Command(bin, "-t", "-q", "-e", filepath.Join(d.Logs, "startup.log"), "-c", confPath).CombinedOutput()
	if err != nil {
		t.Fatalf("nginx -t failed: %v\n%s", err, out)
	}
}

func TestSiteConfigReverb(t *testing.T) {
	plain, _ := SiteConfig(testSite())
	if strings.Contains(string(plain), "proxy_pass") {
		t.Error("site without Reverb has a proxy route")
	}
	s := testSite()
	s.ReverbPort = 6001
	conf, err := SiteConfig(s)
	if err != nil {
		t.Fatal(err)
	}
	c := string(conf)
	for _, want := range []string{
		"location ~ ^/apps?/ {",
		"proxy_pass http://127.0.0.1:6001;",
		"proxy_set_header Host foo.localhost;",
		"proxy_set_header Connection $connection_upgrade;",
	} {
		if !strings.Contains(c, want) {
			t.Errorf("missing %q", want)
		}
	}
	// The dotfile rule must still come first.
	if strings.Index(c, "deny all") > strings.Index(c, "proxy_pass") {
		t.Error("Reverb route placed before the dotfile rule")
	}
	s.ReverbPort = 8080
	if _, err := SiteConfig(s); err == nil {
		t.Error("accepted a Reverb port outside the range")
	}
}

func TestSiteConfigS3(t *testing.T) {
	plain, _ := SiteConfig(testSite())
	if strings.Contains(string(plain), "files.foo.localhost") {
		t.Error("site without S3 has a files server")
	}
	s := testSite()
	s.S3, s.S3Addr = true, "127.0.0.1:3900"
	conf, err := SiteConfig(s)
	if err != nil {
		t.Fatal(err)
	}
	c := string(conf)
	for _, want := range []string{
		"server_name files.foo.localhost;",
		"location ^~ /rig-foo/ {",
		"location = /rig-foo {",
		"proxy_pass http://127.0.0.1:3900;",
		"proxy_set_header Host files.foo.localhost;",
		"ssl_certificate     /home/u/.local/share/rig/certs/foo.crt;",
	} {
		if !strings.Contains(c, want) {
			t.Errorf("missing %q", want)
		}
	}
	// Anything but this site's bucket is refused at nginx.
	filesPart := c[strings.Index(c, "server_name files.foo.localhost;"):]
	if !strings.Contains(filesPart, "location / {\n        return 404;") {
		t.Error("files server forwards more than the site's bucket")
	}
	if strings.Count(c, "proxy_pass http://127.0.0.1:3900;") != 2 {
		t.Error("unexpected number of Garage routes")
	}
	for _, bad := range []string{"0.0.0.0:3900", "example.com:3900", "127.0.0.1:3900;evil", ""} {
		s.S3Addr = bad
		if _, err := SiteConfig(s); err == nil {
			t.Errorf("accepted S3 address %q", bad)
		}
	}
}
