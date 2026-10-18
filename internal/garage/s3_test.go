package garage

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// TestSignV4AWSExample checks the signer against AWS's published example
// "GET Object" (Signature Version 4 docs, examplebucket / test.txt).
func TestSignV4AWSExample(t *testing.T) {
	headers := map[string]string{
		"host":                 "examplebucket.s3.amazonaws.com",
		"range":                "bytes=0-9",
		"x-amz-content-sha256": "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		"x-amz-date":           "20130524T000000Z",
	}
	got := signV4("GET", "/test.txt", "", headers, headers["x-amz-content-sha256"], "us-east-1",
		"AKIAIOSFODNN7EXAMPLE", "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY")
	want := "AWS4-HMAC-SHA256 Credential=AKIAIOSFODNN7EXAMPLE/20130524/us-east-1/s3/aws4_request, " +
		"SignedHeaders=host;range;x-amz-content-sha256;x-amz-date, " +
		"Signature=f0e8bdb87c964420e857bd35b5d6ed310bd44f0170aba48dd91039c6036bdb41"
	if got != want {
		t.Errorf("signature mismatch:\n got  %s\n want %s", got, want)
	}
}

func TestURIEncode(t *testing.T) {
	cases := map[string]string{
		"/rig-foo/bills/a b.pdf": "/rig-foo/bills/a%20b.pdf",
		"/rig-foo/čaj~1.txt":     "/rig-foo/%C4%8Daj~1.txt",
		"/rig-foo/a+b=c&d":       "/rig-foo/a%2Bb%3Dc%26d",
	}
	for in, want := range cases {
		if got := uriEncode(in, false); got != want {
			t.Errorf("uriEncode(%q) = %q, want %q", in, got, want)
		}
	}
	if got := uriEncode("a/b", true); got != "a%2Fb" {
		t.Errorf("slash not encoded: %q", got)
	}
}

func TestCheckKey(t *testing.T) {
	for _, ok := range []string{"a.txt", "bills/2026/x y.pdf", "čaj.txt", "../weird"} {
		if err := CheckKey(ok); err != nil {
			t.Errorf("CheckKey(%q) = %v", ok, err)
		}
	}
	for _, bad := range []string{"", "/abs", "a\nb", "a\x1b[31mred", strings.Repeat("x", 1025), "\xff"} {
		if err := CheckKey(bad); err == nil {
			t.Errorf("CheckKey(%q) accepted", bad)
		}
	}
}

// s3OnRealGarage is called from TestAdminOnRealGarage with a ready bucket
// and key, so only one throwaway Garage is started.
func s3OnRealGarage(t *testing.T, addr, keyID, secret string) {
	t.Helper()
	c := NewS3(keyID, secret)
	c.Addr = addr
	names := []string{"bills/2026/a b.pdf", "bills/čaj.txt", "top.txt", "odd+name=1&x.txt"}
	for i, n := range names {
		body := []byte(fmt.Sprintf("content %d", i))
		if err := c.Put("rig-foo", n, bytes.NewReader(body), int64(len(body))); err != nil {
			t.Fatalf("Put(%q): %v", n, err)
		}
	}
	all, err := c.List("rig-foo", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) < len(names) {
		t.Fatalf("List found %d objects, want >= %d", len(all), len(names))
	}
	bills, _ := c.List("rig-foo", "bills/")
	if len(bills) != 2 {
		t.Errorf("List(bills/) = %d objects, want 2", len(bills))
	}
	c.MaxKeys = 1 // force several pages
	paged, err := c.List("rig-foo", "")
	if err != nil || len(paged) != len(all) {
		t.Errorf("paged List = %d objects, %v; want %d", len(paged), err, len(all))
	}
	c.MaxKeys = 0

	var got bytes.Buffer
	if err := c.Get("rig-foo", "bills/čaj.txt", &got); err != nil || got.String() != "content 1" {
		t.Errorf("Get = %q, %v", got.String(), err)
	}
	if err := c.Get("rig-foo", "missing.txt", &got); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get(missing) = %v, want ErrNotFound", err)
	}
	if err := c.Delete("rig-foo", "top.txt"); err != nil {
		t.Fatal(err)
	}
	if err := c.Get("rig-foo", "top.txt", &got); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get after Delete = %v", err)
	}
	other := NewS3(keyID, secret)
	other.Addr = addr
	if err := other.Put("rig-bar", "x.txt", strings.NewReader("x"), 1); err == nil {
		t.Error("key wrote into another bucket")
	}
	bad := NewS3(keyID, strings.Repeat("0", 64))
	bad.Addr = addr
	if _, err := bad.List("rig-foo", ""); err == nil {
		t.Error("wrong secret was accepted")
	}
}
