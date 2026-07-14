package statefile

import (
	"path/filepath"
	"testing"
)

func TestWriteAndRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "state.json")
	want := struct {
		Version int      `json:"version"`
		Items   []string `json:"items"`
	}{Version: 1, Items: []string{"one", "two"}}
	if err := Write(path, want); err != nil {
		t.Fatal(err)
	}
	var got struct {
		Version int      `json:"version"`
		Items   []string `json:"items"`
	}
	if err := Read(path, &got); err != nil {
		t.Fatal(err)
	}
	if got.Version != want.Version || len(got.Items) != 2 {
		t.Fatalf("unexpected state: %+v", got)
	}
}
