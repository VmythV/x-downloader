package jobs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestFinalizeDownloadRenamesOnSameDevice(t *testing.T) {
	root := t.TempDir()
	sourcePath := filepath.Join(root, "download.part.mp4")
	outputPath := filepath.Join(root, "download.mp4")
	content := []byte("same-device media")
	if err := os.WriteFile(sourcePath, content, 0o640); err != nil {
		t.Fatal(err)
	}

	if err := finalizeDownload(context.Background(), sourcePath, outputPath); err != nil {
		t.Fatalf("finalize download: %v", err)
	}
	assertFinalizedDownload(t, sourcePath, outputPath, content)
}

func TestFinalizeDownloadCopiesAcrossDevices(t *testing.T) {
	root := t.TempDir()
	sourceDir := filepath.Join(root, "source")
	outputDir := filepath.Join(root, "destination")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		t.Fatal(err)
	}

	sourcePath := filepath.Join(sourceDir, "download.part.mp4")
	outputPath := filepath.Join(outputDir, "download.mp4")
	content := []byte("cross-device media")
	if err := os.WriteFile(sourcePath, content, 0o640); err != nil {
		t.Fatal(err)
	}

	renameCalls := 0
	err := finalizeDownloadWithRename(context.Background(), sourcePath, outputPath, func(oldPath, newPath string) error {
		renameCalls++
		return &os.LinkError{Op: "rename", Old: oldPath, New: newPath, Err: syscall.EXDEV}
	})
	if err != nil {
		t.Fatalf("finalize cross-device download: %v", err)
	}
	if renameCalls != 1 {
		t.Fatalf("rename calls = %d, want 1", renameCalls)
	}
	assertFinalizedDownload(t, sourcePath, outputPath, content)

	stagingFiles, err := filepath.Glob(filepath.Join(outputDir, ".*.partial"))
	if err != nil {
		t.Fatal(err)
	}
	if len(stagingFiles) != 0 {
		t.Fatalf("destination staging files were not cleaned up: %v", stagingFiles)
	}
}

func TestFinalizeDownloadDoesNotCopyForOtherRenameErrors(t *testing.T) {
	root := t.TempDir()
	sourcePath := filepath.Join(root, "download.part.mp4")
	outputPath := filepath.Join(root, "download.mp4")
	if err := os.WriteFile(sourcePath, []byte("media"), 0o600); err != nil {
		t.Fatal(err)
	}

	renameErr := errors.New("permission denied")
	err := finalizeDownloadWithRename(context.Background(), sourcePath, outputPath, func(_, _ string) error {
		return renameErr
	})
	if !errors.Is(err, renameErr) {
		t.Fatalf("finalize error = %v, want %v", err, renameErr)
	}
	if _, err := os.Stat(sourcePath); err != nil {
		t.Fatalf("source should remain after a non-cross-device rename error: %v", err)
	}
	if _, err := os.Stat(outputPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("output should not exist, stat error = %v", err)
	}
}

func TestFinalizeDownloadHonorsCancellation(t *testing.T) {
	root := t.TempDir()
	sourcePath := filepath.Join(root, "download.part.mp4")
	outputPath := filepath.Join(root, "download.mp4")
	if err := os.WriteFile(sourcePath, []byte("media"), 0o600); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := finalizeDownload(ctx, sourcePath, outputPath)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("finalize error = %v, want context cancellation", err)
	}
	if _, err := os.Stat(sourcePath); err != nil {
		t.Fatalf("source should remain after cancellation: %v", err)
	}
	if _, err := os.Stat(outputPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("output should not exist, stat error = %v", err)
	}
}

func assertFinalizedDownload(t *testing.T, sourcePath, outputPath string, want []byte) {
	t.Helper()

	if _, err := os.Stat(sourcePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("source should be removed, stat error = %v", err)
	}
	got, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("output content = %q, want %q", got, want)
	}
}
