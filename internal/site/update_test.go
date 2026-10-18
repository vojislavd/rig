package site

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestUpdateSaves(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sites.json")

	err := Update(path, func(r *Registry) error {
		return r.Add(Site{Name: "foo", Dir: "/home/u/code/foo"})
	})
	if err != nil {
		t.Fatal(err)
	}

	r, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := r.Get("foo"); !ok {
		t.Error("foo not saved")
	}
}

func TestUpdateDoesNotSaveOnError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sites.json")
	boom := errors.New("boom")

	err := Update(path, func(r *Registry) error {
		r.Add(Site{Name: "foo", Dir: "/home/u/code/foo"})
		return boom // fail after changing the registry
	})
	if !errors.Is(err, boom) {
		t.Fatalf("Update = %v, want boom", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Error("sites.json was written even though fn failed")
	}
}

func TestUpdateConcurrent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sites.json")
	const n = 20

	// Start n goroutines (lightweight threads) that each add one site at
	// the same time. Without the lock, some additions would be lost.
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			name := fmt.Sprintf("site%d", i)
			errs <- Update(path, func(r *Registry) error {
				return r.Add(Site{Name: name, Dir: "/home/u/code/" + name})
			})
		}()
	}
	wg.Wait() // wait for all goroutines to finish
	close(errs)
	for err := range errs {
		if err != nil {
			t.Error(err)
		}
	}

	r, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(r.List()); got != n {
		t.Errorf("got %d sites, want %d (updates were lost)", got, n)
	}
}
