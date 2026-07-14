package settings

import (
	"context"
	"path/filepath"
	"testing"

	"x-downloader/helper/internal/folderpicker"
	"x-downloader/helper/internal/statefile"
)

type targetRecorder struct{ values Values }

func (target *targetRecorder) SetDownloadDir(path string) { target.values.DownloadDir = path }
func (target *targetRecorder) SetFilenameTemplate(value string) {
	target.values.FilenameTemplate = value
}
func (target *targetRecorder) SetConcurrency(value int) { target.values.Concurrency = value }
func (target *targetRecorder) SetRetryCount(value int)  { target.values.RetryCount = value }

func testDefaults(root string) Defaults {
	return Defaults{
		DownloadDir: filepath.Join(root, "default"), FilenameTemplate: "{mediaId}.{ext}",
		Concurrency: 1, RetryCount: 1,
	}
}

func TestManagerPersistsAndAppliesSettings(t *testing.T) {
	root := t.TempDir()
	selectedDir := filepath.Join(root, "selected")
	statePath := filepath.Join(root, "state", "settings.json")
	manager, err := New(statePath, testDefaults(root), folderpicker.PickerFunc(func(context.Context) (string, error) {
		return selectedDir, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	target := &targetRecorder{}
	manager.Bind(target)
	if target.values.DownloadDir != filepath.Join(root, "default") || target.values.RetryCount != 1 {
		t.Fatalf("unexpected bound settings: %+v", target.values)
	}
	picked, err := manager.PickDownloadDirectory(context.Background())
	if err != nil || picked != selectedDir {
		t.Fatalf("unexpected picker result: %q, %v", picked, err)
	}
	template := "{author}_{mediaId}_{height}p.{ext}"
	concurrency := 3
	retryCount := 2
	updated, err := manager.Update(Update{
		DownloadDir: &picked, FilenameTemplate: &template, Concurrency: &concurrency, RetryCount: &retryCount,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.DownloadDir != selectedDir || target.values.Concurrency != 3 || target.values.RetryCount != 2 || target.values.FilenameTemplate != template {
		t.Fatalf("settings were not applied: %+v, target=%+v", updated, target.values)
	}

	restored, err := New(statePath, testDefaults(root), nil)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Get().Values != updated.Values {
		t.Fatalf("settings were not restored: %+v", restored.Get())
	}
}

func TestManagerMigratesVersionOneDirectorySetting(t *testing.T) {
	root := t.TempDir()
	statePath := filepath.Join(root, "settings.json")
	selectedDir := filepath.Join(root, "legacy")
	if err := statefile.Write(statePath, map[string]any{"version": 1, "downloadDir": selectedDir}); err != nil {
		t.Fatal(err)
	}
	manager, err := New(statePath, testDefaults(root), nil)
	if err != nil {
		t.Fatal(err)
	}
	if manager.Get().DownloadDir != selectedDir || manager.Get().FilenameTemplate != "{mediaId}.{ext}" {
		t.Fatalf("version one settings were not migrated: %+v", manager.Get())
	}
}

func TestManagerDoesNotChangeSettingsWhenPickerIsCancelled(t *testing.T) {
	root := t.TempDir()
	manager, err := New(filepath.Join(root, "settings.json"), testDefaults(root), folderpicker.PickerFunc(func(context.Context) (string, error) {
		return "", folderpicker.ErrCancelled
	}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.PickDownloadDirectory(context.Background()); err != ErrSelectionCancelled {
		t.Fatalf("unexpected cancellation error: %v", err)
	}
}

func TestManagerRejectsInvalidDownloadRulesWithoutChangingCurrentSettings(t *testing.T) {
	root := t.TempDir()
	manager, err := New(filepath.Join(root, "settings.json"), testDefaults(root), nil)
	if err != nil {
		t.Fatal(err)
	}
	original := manager.Get().Values
	badTemplate := "nested/{mediaId}.{ext}"
	concurrency := 5
	if _, err := manager.Update(Update{FilenameTemplate: &badTemplate}); err == nil {
		t.Fatal("expected template with a path separator to be rejected")
	}
	if _, err := manager.Update(Update{Concurrency: &concurrency}); err == nil {
		t.Fatal("expected concurrency above four to be rejected")
	}
	if manager.Get().Values != original {
		t.Fatalf("invalid update changed settings: %+v", manager.Get())
	}
}
