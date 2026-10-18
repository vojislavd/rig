package stack

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"github.com/vojislavd/rig/internal/garage"
	"github.com/vojislavd/rig/internal/site"
)

// s3Client is the part of garage.S3 the stack uses; tests use a fake.
type s3Client interface {
	List(bucket, prefix string) ([]garage.Object, error)
	Get(bucket, key string, w io.Writer) error
	Put(bucket, key string, r io.Reader, size int64) error
	Delete(bucket, key string) error
}

var newS3Client = func(keyID, secret string) s3Client { return garage.NewS3(keyID, secret) }

// siteS3 returns an S3 client using the site's own key (allowed on its
// bucket only) and the bucket name, after checking the site has S3 and
// Garage runs.
func (st *Stack) siteS3(name string) (s3Client, string, error) {
	r, err := site.Load(st.L.Registry())
	if err != nil {
		return nil, "", err
	}
	s, ok := r.Get(name)
	if !ok {
		return nil, "", fmt.Errorf("%w: %q", site.ErrNotFound, name)
	}
	if !s.S3 {
		return nil, "", fmt.Errorf("%s has no S3 bucket (rig s3 enable %s)", name, name)
	}
	id, secret, err := st.s3Credentials(name)
	if err != nil {
		return nil, "", err
	}
	return newS3Client(id, secret), site.Bucket(name), nil
}

// S3List lists the site's files whose name starts with prefix.
func (st *Stack) S3List(name, prefix string) ([]garage.Object, error) {
	c, bucket, err := st.siteS3(name)
	if err != nil {
		return nil, err
	}
	return c.List(bucket, prefix)
}

// S3Get downloads key to dest and returns the path written. dest defaults
// to the file name part of key in the current folder; if dest is a
// folder, the file goes inside it. An existing file is only replaced with
// force, never through a symlink. The file appears only once complete.
func (st *Stack) S3Get(name, key, dest string, force bool) (string, error) {
	if err := garage.CheckKey(key); err != nil {
		return "", err
	}
	base := filepath.Base(key)
	if base == "." || base == ".." || base == "/" {
		return "", fmt.Errorf("can't make a file name from %q", key)
	}
	if dest == "" {
		dest = base
	} else if info, err := os.Stat(dest); err == nil && info.IsDir() {
		dest = filepath.Join(dest, base)
	}
	if info, err := os.Lstat(dest); err == nil {
		switch {
		case !info.Mode().IsRegular():
			return "", fmt.Errorf("%s exists and is not a regular file; refusing to replace it", dest)
		case !force:
			return "", fmt.Errorf("%s already exists (use --force to replace it)", dest)
		}
	}

	c, bucket, err := st.siteS3(name)
	if err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dest), ".rig-download-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name()) // no-op after the rename
	if err := c.Get(bucket, key, tmp); err != nil {
		tmp.Close()
		return "", err
	}
	if err := errors.Join(tmp.Sync(), tmp.Close()); err != nil {
		return "", err
	}
	if err := os.Rename(tmp.Name(), dest); err != nil {
		return "", err
	}
	return dest, nil
}

// S3Put uploads the local file src as key and returns its size. Only
// regular files are accepted: no folders, and no symlinks, which could
// pull in a file from somewhere unexpected (like ~/.ssh).
func (st *Stack) S3Put(name, src, key string) (int64, error) {
	if err := garage.CheckKey(key); err != nil {
		return 0, err
	}
	info, err := os.Lstat(src)
	if err != nil {
		return 0, err
	}
	if !info.Mode().IsRegular() {
		return 0, fmt.Errorf("%s is not a regular file (folders and symlinks are refused)", src)
	}
	f, err := os.OpenFile(src, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	c, bucket, err := st.siteS3(name)
	if err != nil {
		return 0, err
	}
	return info.Size(), c.Put(bucket, key, f, info.Size())
}

// S3Remove deletes key from the site's bucket. A missing file is reported
// (S3 itself would quietly accept the delete).
func (st *Stack) S3Remove(name, key string) error {
	if err := garage.CheckKey(key); err != nil {
		return err
	}
	c, bucket, err := st.siteS3(name)
	if err != nil {
		return err
	}
	objs, err := c.List(bucket, key)
	if err != nil {
		return err
	}
	for _, o := range objs {
		if o.Key == key {
			return c.Delete(bucket, key)
		}
	}
	return fmt.Errorf("%s: %w", key, garage.ErrNotFound)
}
