package auth

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadOrCreatePersistsToken(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "token")
	first, err := LoadOrCreate(path)
	if err != nil {
		t.Fatal(err)
	}
	second, err := LoadOrCreate(path)
	if err != nil {
		t.Fatal(err)
	}
	if first != second || len(first) < 32 {
		t.Fatal("token was not persisted")
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("unexpected token permissions: %o", info.Mode().Perm())
	}
}
