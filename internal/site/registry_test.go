package site

import (
	"errors"
	"testing"
)

func TestRegistryAddGetRemove(t *testing.T) {
	r := NewRegistry()
	foo := Site{Name: "foo", Dir: "/home/u/code/foo"}

	if err := r.Add(foo); err != nil {
		t.Fatalf("Add(foo): %v", err)
	}
	if got, ok := r.Get("foo"); !ok || got != foo {
		t.Fatalf("Get(foo) = %v, %v; want %v, true", got, ok, foo)
	}

	// Adding the same name again must fail with ErrExists.
	if err := r.Add(foo); !errors.Is(err, ErrExists) {
		t.Errorf("second Add(foo) = %v, want ErrExists", err)
	}

	if err := r.Remove("foo"); err != nil {
		t.Fatalf("Remove(foo): %v", err)
	}
	if _, ok := r.Get("foo"); ok {
		t.Error("Get(foo) after Remove: still present")
	}
	if err := r.Remove("foo"); !errors.Is(err, ErrNotFound) {
		t.Errorf("second Remove(foo) = %v, want ErrNotFound", err)
	}
}

func TestRegistryAddRejects(t *testing.T) {
	tests := []struct {
		name string
		site Site
	}{
		{"invalid name", Site{Name: "Foo", Dir: "/home/u/code/Foo"}},
		{"reserved name", Site{Name: "rig", Dir: "/home/u/code/rig"}},
		{"relative dir", Site{Name: "foo", Dir: "code/foo"}},
		{"unclean dir", Site{Name: "foo", Dir: "/home/u/code/../code/foo"}},
		{"folder name differs", Site{Name: "foo", Dir: "/home/u/code/bar"}},
		{"empty dir", Site{Name: "foo", Dir: ""}},
	}
	for _, tt := range tests {
		r := NewRegistry()
		if err := r.Add(tt.site); err == nil {
			t.Errorf("%s: Add(%+v) = nil, want error", tt.name, tt.site)
		}
	}
}

func TestRegistryListSorted(t *testing.T) {
	r := NewRegistry()
	for _, n := range []string{"zeta", "alpha", "mid"} {
		if err := r.Add(Site{Name: n, Dir: "/home/u/code/" + n}); err != nil {
			t.Fatal(err)
		}
	}
	got := r.List()
	want := []string{"alpha", "mid", "zeta"}
	if len(got) != len(want) {
		t.Fatalf("List() has %d sites, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].Name != want[i] {
			t.Errorf("List()[%d] = %q, want %q", i, got[i].Name, want[i])
		}
	}
}

func TestRegistryReverbPort(t *testing.T) {
	r := NewRegistry()
	for _, n := range []string{"a", "b"} {
		if err := r.Add(Site{Name: n, Dir: "/home/u/code/" + n}); err != nil {
			t.Fatal(err)
		}
	}
	pa, changed, err := r.ReverbPort("a")
	if err != nil || pa != MinReverbPort || !changed {
		t.Fatalf("ReverbPort(a) = %d, %v, %v", pa, changed, err)
	}
	if again, changed, _ := r.ReverbPort("a"); again != pa || changed {
		t.Errorf("second call = %d, %v; want same port, unchanged", again, changed)
	}
	if pb, _, _ := r.ReverbPort("b"); pb == pa {
		t.Error("two sites got the same port")
	}
	if _, _, err := r.ReverbPort("nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown site: %v", err)
	}
	for _, bad := range []Site{
		{Name: "c", Dir: "/home/u/code/c", ReverbPort: 80},
		{Name: "c", Dir: "/home/u/code/c", ReverbPort: pa}, // taken
	} {
		if err := r.Add(bad); err == nil {
			t.Errorf("Add accepted %+v", bad)
		}
	}
}
