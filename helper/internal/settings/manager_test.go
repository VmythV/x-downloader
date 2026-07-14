package settings

import (
	"context"
	"path/filepath"
	"testing"

	"x-downloader/helper/internal/folderpicker"
)

type targetRecorder struct{ path string }

func (target *targetRecorder) SetDownloadDir(path string) { target.path = path }

func TestManagerPersistsAndAppliesDownloadDirectory(t *testing.T) {
	root := t.TempDir()
	defaultDir := filepath.Join(root, "default")
	selectedDir := filepath.Join(root, "selected")
	statePath := filepath.Join(root, "state", "settings.json")
	manager, err := New(statePath, defaultDir, folderpicker.PickerFunc(func(context.Context) (string, error) {
		return selectedDir, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	target := &targetRecorder{}
	manager.Bind(target)
	if target.path != defaultDir {
		t.Fatalf("unexpected bound directory: %s", target.path)
	}
	picked, err := manager.PickDownloadDirectory(context.Background())
	if err != nil || picked != selectedDir {
		t.Fatalf("unexpected picker result: %q, %v", picked, err)
	}
	updated, err := manager.UpdateDownloadDir(picked)
	if err != nil {
		t.Fatal(err)
	}
	if updated.DownloadDir != selectedDir || target.path != selectedDir {
		t.Fatalf("directory was not applied: %+v, target=%s", updated, target.path)
	}

	restored, err := New(statePath, defaultDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Get().DownloadDir != selectedDir {
		t.Fatalf("directory was not restored: %+v", restored.Get())
	}
}

func TestManagerDoesNotChangeDirectoryWhenPickerIsCancelled(t *testing.T) {
	root := t.TempDir()
	manager, err := New(filepath.Join(root, "settings.json"), filepath.Join(root, "default"), folderpicker.PickerFunc(func(context.Context) (string, error) {
		return "", folderpicker.ErrCancelled
	}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.PickDownloadDirectory(context.Background()); err != ErrSelectionCancelled {
		t.Fatalf("unexpected cancellation error: %v", err)
	}
}
