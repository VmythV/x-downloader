package downloadpath

import (
	"path/filepath"
	"testing"
)

func TestPrepareCreatesWritableAbsoluteDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "downloads")
	prepared, err := Prepare(path)
	if err != nil {
		t.Fatal(err)
	}
	if prepared != path || !Writable(path) {
		t.Fatalf("unexpected prepared directory: %q", prepared)
	}
}

func TestNormalizeRejectsRelativeDirectory(t *testing.T) {
	if _, err := Normalize("relative/downloads"); err == nil {
		t.Fatal("expected relative directory to be rejected")
	}
}
