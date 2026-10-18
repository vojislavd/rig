package site

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// SiteDir checks that dir is a real folder directly inside codeDir and
// returns its fully resolved path. Both arguments must be absolute.
//
// Security: rig writes config pointing at this folder and may later delete
// it (rig rm --purge), so it must never end up outside the code directory.
// Every symlink is resolved first, then the result is compared, so tricks
// like "code/../etc" or a symlink "code/x -> /etc" are caught.
//
// The check can't stop the folder from being swapped for a symlink right
// after it runs. That is acceptable: anything able to do that already runs
// as the user, and rig never runs with more rights than the user (§3).
func SiteDir(codeDir, dir string) (string, error) {
	if !filepath.IsAbs(codeDir) || !filepath.IsAbs(dir) {
		return "", errors.New("code dir and site dir must be absolute paths")
	}

	// EvalSymlinks follows every symlink and cleans ".." and "." parts,
	// giving the real location on disk. It fails if the path doesn't exist.
	realCode, err := filepath.EvalSymlinks(codeDir)
	if err != nil {
		return "", fmt.Errorf("code dir: %w", err)
	}
	realDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", fmt.Errorf("site dir: %w", err)
	}

	// Folder name = site name, so only direct children are allowed.
	if filepath.Dir(realDir) != realCode {
		return "", fmt.Errorf("site dir %q must be directly inside %q", realDir, realCode)
	}

	info, err := os.Stat(realDir)
	if err != nil {
		return "", fmt.Errorf("site dir: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("site dir %q is not a folder", realDir)
	}

	return realDir, nil
}
