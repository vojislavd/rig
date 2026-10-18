package garage

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestConfigAndSecrets(t *testing.T) {
	sec, err := NewSecrets()
	if err != nil {
		t.Fatal(err)
	}
	conf, err := Config("/home/u/.local/share/rig/garage", sec)
	if err != nil {
		t.Fatal(err)
	}
	s := string(conf)
	for _, want := range []string{
		`api_bind_addr = "127.0.0.1:3900"`,
		`rpc_bind_addr = "127.0.0.1:3901"`,
		`api_bind_addr = "127.0.0.1:3903"`,
		`metadata_dir = "/home/u/.local/share/rig/garage/meta"`,
		`replication_factor = 1`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("config missing %q", want)
		}
	}
	if strings.Contains(s, "0.0.0.0") || strings.Contains(s, "[::]") {
		t.Error("config listens beyond loopback")
	}

	path := filepath.Join(t.TempDir(), "garage.toml")
	os.WriteFile(path, conf, 0o600)
	got, err := LoadSecrets(path)
	if err != nil || got != sec {
		t.Errorf("LoadSecrets = %+v, %v", got, err)
	}
	if _, err := Config("/home/u/x", Secrets{RPC: "short"}); err == nil {
		t.Error("accepted bad secrets")
	}
	if _, err := Config("/home/u/my data", sec); err == nil {
		t.Error("accepted an unsafe path")
	}
}

func TestUnit(t *testing.T) {
	unit, err := Unit("/home/u/.config/rig/garage.toml", "/home/u/.local/share/rig/garage")
	if err != nil {
		t.Fatal(err)
	}
	s := string(unit)
	for _, want := range []string{
		"ExecStart=/usr/bin/garage -c /home/u/.config/rig/garage.toml server\n",
		"ProtectHome=tmpfs\n",
		"BindPaths=/home/u/.local/share/rig/garage\n",
		"BindReadOnlyPaths=/home/u/.config/rig/garage.toml\n",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("unit missing %q", want)
		}
	}
	for _, banned := range []string{"[Install]", "WantedBy"} {
		if strings.Contains(s, banned) {
			t.Errorf("unit contains %q (must be on demand)", banned)
		}
	}
}

// TestAdminOnRealGarage starts a throwaway Garage on test ports (loopback,
// temp folder, stopped afterwards) and runs the admin client against it.
func TestAdminOnRealGarage(t *testing.T) {
	if _, err := exec.LookPath("garage"); err != nil {
		t.Skip("garage not installed")
	}
	if testing.Short() {
		t.Skip("starts a Garage server")
	}
	dir, err := os.MkdirTemp("", "riggarage")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	for _, d := range DataDirs(dir) {
		os.MkdirAll(d, 0o700)
	}
	sec, _ := NewSecrets()
	const s3, rpc, admin = "127.0.0.1:23900", "127.0.0.1:23901", "127.0.0.1:23903"
	conf, err := config(dir, sec, s3, rpc, admin)
	if err != nil {
		t.Fatal(err)
	}
	confPath := filepath.Join(dir, "garage.toml")
	os.WriteFile(confPath, conf, 0o600)
	srv := exec.Command("garage", "-c", confPath, "server")
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Process.Kill(); srv.Wait() })

	a := NewAdmin(admin, sec.Admin)
	if err := a.WaitReady(15 * time.Second); err != nil {
		t.Fatal(err)
	}
	if err := a.InitLayout(); err != nil {
		t.Fatal(err)
	}
	if err := a.InitLayout(); err != nil {
		t.Fatalf("second InitLayout: %v", err)
	}

	bucket, err := a.Bucket("rig-foo")
	if err != nil || len(bucket) != 64 {
		t.Fatalf("Bucket = %q, %v", bucket, err)
	}
	if again, _ := a.Bucket("rig-foo"); again != bucket {
		t.Error("Bucket created a second bucket for the same alias")
	}
	keyID, secret, err := a.Key("rig-foo")
	if err != nil || !strings.HasPrefix(keyID, "GK") || len(secret) != 64 {
		t.Fatalf("Key = %q, len(secret)=%d, %v", keyID, len(secret), err)
	}
	if id2, sec2, _ := a.Key("rig-foo"); id2 != keyID || sec2 != secret {
		t.Error("Key made a new key instead of returning the existing one")
	}
	if err := a.Allow(bucket, keyID); err != nil {
		t.Fatal(err)
	}

	// Real S3 with that key: allowed on its bucket, refused on another.
	if _, err := a.Bucket("rig-bar"); err != nil {
		t.Fatal(err)
	}
	if code := s3Put(t, s3, keyID, secret, "rig-foo", "hello.txt"); code != 200 {
		t.Errorf("PUT into own bucket = %d", code)
	}
	if code := s3Put(t, s3, keyID, secret, "rig-bar", "x.txt"); code != 403 {
		t.Errorf("PUT into another bucket = %d, want 403", code)
	}
	s3OnRealGarage(t, s3, keyID, secret)

	bad := NewAdmin(admin, strings.Repeat("0", 64))
	if _, err := bad.Status(); err == nil {
		t.Error("admin API accepted a wrong token")
	}
}

// s3Put does a minimal AWS Signature V4 PUT, returning the HTTP status.
func s3Put(t *testing.T, addr, keyID, secret, bucket, object string) int {
	t.Helper()
	body := []byte("hello")
	now := time.Now().UTC()
	amzDate, day := now.Format("20060102T150405Z"), now.Format("20060102")
	payloadHash := sha256Hex(body)
	path := "/" + bucket + "/" + object
	canonical := strings.Join([]string{"PUT", path, "",
		"host:" + addr, "x-amz-content-sha256:" + payloadHash, "x-amz-date:" + amzDate, "",
		"host;x-amz-content-sha256;x-amz-date", payloadHash}, "\n")
	scope := day + "/" + Region + "/s3/aws4_request"
	toSign := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + scope + "\n" + sha256Hex([]byte(canonical))
	k := hmacSHA256([]byte("AWS4"+secret), day)
	for _, part := range []string{Region, "s3", "aws4_request"} {
		k = hmacSHA256(k, part)
	}
	sig := hex.EncodeToString(hmacSHA256(k, toSign))
	req, _ := http.NewRequest(http.MethodPut, "http://"+addr+path, bytes.NewReader(body))
	req.Header.Set("x-amz-date", amzDate)
	req.Header.Set("x-amz-content-sha256", payloadHash)
	req.Header.Set("Authorization", fmt.Sprintf("AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=host;x-amz-content-sha256;x-amz-date, Signature=%s", keyID, scope, sig))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func sha256Hex(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
