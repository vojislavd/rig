package site

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"time"

	"github.com/vojislavd/rig/internal/fsutil"
)

// fileVersion is the sites.json format version. Bump it when the format
// changes, so an old rig refuses a newer file instead of misreading it.
const fileVersion = 1

// maxFileSize caps how much Load reads. sites.json is tiny; anything
// bigger is corrupt or hostile, and shouldn't exhaust memory.
const maxFileSize = 1 << 20 // 1 MiB

// file is the on-disk shape of sites.json.
type file struct {
	Version int    `json:"version"`
	Sites   []Site `json:"sites"`
}

// Load reads the registry from path. A missing file means no sites yet.
//
// Security: the file is never trusted. Every entry goes through Add, so a
// hand-edited or corrupted file can't smuggle in a bad name, a path
// outside the rules, or a duplicate.
func Load(path string) (*Registry, error) {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return NewRegistry(), nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close() // runs when Load returns

	// Read one byte past the limit, so we can tell "too big" apart from
	// "exactly at the limit".
	data, err := io.ReadAll(io.LimitReader(f, maxFileSize+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxFileSize {
		return nil, fmt.Errorf("%s: file larger than %d bytes", path, maxFileSize)
	}

	var parsed file
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields() // a typo'd or unexpected key is an error
	if err := dec.Decode(&parsed); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if dec.More() {
		return nil, fmt.Errorf("%s: unexpected data after the JSON object", path)
	}
	if parsed.Version != fileVersion {
		return nil, fmt.Errorf("%s: unsupported version %d (want %d)", path, parsed.Version, fileVersion)
	}

	r := NewRegistry()
	for _, s := range parsed.Sites {
		if err := r.Add(s); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
	}
	return r, nil
}

// lockTimeout is how long Update waits for another rig command to finish.
const lockTimeout = 5 * time.Second

// Update is the only safe way to change the registry on disk: it locks
// path+".lock", loads the registry, calls fn, and saves only if fn
// succeeds. The lock stops two rig commands from overwriting each
// other's changes.
func Update(path string, fn func(*Registry) error) (err error) {
	unlock, err := fsutil.Lock(path+".lock", lockTimeout)
	if err != nil {
		return err
	}
	defer func() {
		// Report an unlock failure only if nothing else went wrong.
		if uerr := unlock(); err == nil {
			err = uerr
		}
	}()

	r, err := Load(path)
	if err != nil {
		return err
	}
	if err := fn(r); err != nil {
		return err
	}
	return r.Save(path)
}

// Save writes the registry to path atomically with mode 0600. The folder
// must already exist. Sites are written sorted, so the file is stable.
// Callers changing the registry should use Update, which also locks.
func (r *Registry) Save(path string) error {
	data, err := json.MarshalIndent(file{Version: fileVersion, Sites: r.List()}, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return fsutil.WriteFileAtomic(path, data, 0o600)
}
