package storage

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestStoreSetGetListRemoveAndResolveRef(t *testing.T) {
	configRoot := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configRoot)

	store, err := NewStore("reflet-storage-test")
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}

	cfg, err := store.Config()
	if err != nil {
		t.Fatalf("Config() error = %v", err)
	}
	if want := filepath.Join(configRoot, "reflet", "keyring"); cfg.FileDir != want {
		t.Fatalf("Config().FileDir = %q, want %q", cfg.FileDir, want)
	}

	if err := store.Set("alpha", "secret-a"); err != nil {
		t.Fatalf("Set(alpha) error = %v", err)
	}
	if err := store.Set("beta", "secret-b"); err != nil {
		t.Fatalf("Set(beta) error = %v", err)
	}

	got, err := store.Get("alpha")
	if err != nil {
		t.Fatalf("Get(alpha) error = %v", err)
	}
	if got != "secret-a" {
		t.Fatalf("Get(alpha) = %q, want %q", got, "secret-a")
	}

	keys, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if want := []string{"alpha", "beta"}; !reflect.DeepEqual(keys, want) {
		t.Fatalf("List() = %v, want %v", keys, want)
	}

	resolved, err := store.ResolveRef("ref://beta")
	if err != nil {
		t.Fatalf("ResolveRef() error = %v", err)
	}
	if resolved != "secret-b" {
		t.Fatalf("ResolveRef() = %q, want %q", resolved, "secret-b")
	}

	if err := store.Remove("alpha"); err != nil {
		t.Fatalf("Remove(alpha) error = %v", err)
	}
	if _, err := store.Get("alpha"); err == nil {
		t.Fatal("Get(alpha) after Remove() error = nil, want non-nil")
	}
}
