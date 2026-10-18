package stack

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/vojislavd/rig/internal/garage"
)

// memS3 is an in-memory bucket store.
type memS3 struct{ objs map[string][]byte }

func (m *memS3) List(bucket, prefix string) ([]garage.Object, error) {
	var out []garage.Object
	for k, v := range m.objs {
		if strings.HasPrefix(k, bucket+"/"+prefix) {
			out = append(out, garage.Object{Key: strings.TrimPrefix(k, bucket+"/"), Size: int64(len(v)), LastModified: time.Now()})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}
func (m *memS3) Get(bucket, key string, w io.Writer) error {
	v, ok := m.objs[bucket+"/"+key]
	if !ok {
		return garage.ErrNotFound
	}
	_, err := w.Write(v)
	return err
}
func (m *memS3) Put(bucket, key string, r io.Reader, size int64) error {
	b, err := io.ReadAll(r)
	m.objs[bucket+"/"+key] = b
	return err
}
func (m *memS3) Delete(bucket, key string) error { delete(m.objs, bucket+"/"+key); return nil }

func s3Site(t *testing.T) (*Stack, *memS3) {
	t.Helper()
	st, _ := linkedStack(t)
	withFakeGarage(t)
	if err := st.StartS3(); err != nil {
		t.Fatal(err)
	}
	if err := st.EnableS3("foo"); err != nil {
		t.Fatal(err)
	}
	mem := &memS3{objs: map[string][]byte{}}
	old := newS3Client
	newS3Client = func(string, string) s3Client { return mem }
	t.Cleanup(func() { newS3Client = old })
	return st, mem
}

func TestS3PutListGetRemove(t *testing.T) {
	st, mem := s3Site(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "invoice.pdf")
	os.WriteFile(src, []byte("PDF"), 0o600)

	if n, err := st.S3Put("foo", src, "bills/invoice.pdf"); err != nil || n != 3 {
		t.Fatalf("S3Put = %d, %v", n, err)
	}
	if string(mem.objs["rig-foo/bills/invoice.pdf"]) != "PDF" {
		t.Error("uploaded into the wrong bucket or key")
	}
	list, err := st.S3List("foo", "bills/")
	if err != nil || len(list) != 1 || list[0].Key != "bills/invoice.pdf" {
		t.Errorf("S3List = %+v, %v", list, err)
	}

	// Download into a folder: the file name comes from the key.
	out := t.TempDir()
	got, err := st.S3Get("foo", "bills/invoice.pdf", out, false)
	if err != nil || got != filepath.Join(out, "invoice.pdf") {
		t.Fatalf("S3Get = %q, %v", got, err)
	}
	if b, _ := os.ReadFile(got); !bytes.Equal(b, []byte("PDF")) {
		t.Error("downloaded content differs")
	}
	if _, err := st.S3Get("foo", "bills/invoice.pdf", out, false); err == nil {
		t.Error("overwrote an existing file without --force")
	}
	if _, err := st.S3Get("foo", "bills/invoice.pdf", out, true); err != nil {
		t.Errorf("--force: %v", err)
	}
	entries, _ := os.ReadDir(out)
	if len(entries) != 1 {
		t.Errorf("download folder has %d entries, want 1 (no temp files)", len(entries))
	}

	if err := st.S3Remove("foo", "bills/invoice.pdf"); err != nil {
		t.Fatal(err)
	}
	if err := st.S3Remove("foo", "bills/invoice.pdf"); !errors.Is(err, garage.ErrNotFound) {
		t.Errorf("removing a missing file = %v", err)
	}
}

func TestS3PutRefusesSymlinkAndFolder(t *testing.T) {
	st, mem := s3Site(t)
	dir := t.TempDir()
	secret := filepath.Join(dir, "id_ed25519")
	os.WriteFile(secret, []byte("PRIVATE KEY"), 0o600)
	link := filepath.Join(dir, "innocent.pdf")
	os.Symlink(secret, link)
	if _, err := st.S3Put("foo", link, "x.pdf"); err == nil {
		t.Error("uploaded through a symlink")
	}
	if _, err := st.S3Put("foo", dir, "x"); err == nil {
		t.Error("uploaded a folder")
	}
	if len(mem.objs) != 0 {
		t.Error("something was uploaded")
	}
}

func TestS3GetRefusesSymlinkTarget(t *testing.T) {
	st, mem := s3Site(t)
	mem.objs["rig-foo/a.txt"] = []byte("data")
	dir := t.TempDir()
	victim := filepath.Join(dir, "victim")
	os.WriteFile(victim, []byte("keep"), 0o600)
	link := filepath.Join(dir, "a.txt")
	os.Symlink(victim, link)
	if _, err := st.S3Get("foo", "a.txt", link, true); err == nil {
		t.Error("replaced a symlink")
	}
	if b, _ := os.ReadFile(victim); string(b) != "keep" {
		t.Error("wrote through the symlink")
	}
}

func TestS3NeedsEnabledSite(t *testing.T) {
	st, _ := linkedStack(t)
	withFakeGarage(t)
	st.StartS3()
	if _, err := st.S3List("foo", ""); err == nil || !strings.Contains(err.Error(), "rig s3 enable") {
		t.Errorf("S3List on a site without S3 = %v", err)
	}
}
