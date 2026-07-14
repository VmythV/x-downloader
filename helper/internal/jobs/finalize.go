package jobs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
)

type renameFileFunc func(oldPath, newPath string) error

func finalizeDownload(ctx context.Context, sourcePath, outputPath string) error {
	return finalizeDownloadWithRename(ctx, sourcePath, outputPath, os.Rename)
}

func finalizeDownloadWithRename(ctx context.Context, sourcePath, outputPath string, renameFile renameFileFunc) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := renameFile(sourcePath, outputPath); err == nil {
		return nil
	} else if !isCrossDeviceError(err) {
		return err
	}

	return copyDownloadAcrossDevices(ctx, sourcePath, outputPath)
}

func isCrossDeviceError(err error) bool {
	if errors.Is(err, syscall.EXDEV) {
		return true
	}

	// MoveFileEx returns ERROR_NOT_SAME_DEVICE (Win32 error 17) when the
	// source and destination are on different volumes.
	return runtime.GOOS == "windows" && errors.Is(err, syscall.Errno(17))
}

func copyDownloadAcrossDevices(ctx context.Context, sourcePath, outputPath string) error {
	source, err := os.Open(sourcePath)
	if err != nil {
		return fmt.Errorf("open completed download: %w", err)
	}
	sourceClosed := false
	defer func() {
		if !sourceClosed {
			_ = source.Close()
		}
	}()

	sourceInfo, err := source.Stat()
	if err != nil {
		return fmt.Errorf("inspect completed download: %w", err)
	}

	outputDir := filepath.Dir(outputPath)
	staging, err := os.CreateTemp(outputDir, "."+filepath.Base(outputPath)+".*.partial")
	if err != nil {
		return fmt.Errorf("create destination staging file: %w", err)
	}
	stagingPath := staging.Name()
	stagingClosed := false
	committed := false
	defer func() {
		if !stagingClosed {
			_ = staging.Close()
		}
		if !committed {
			_ = os.Remove(stagingPath)
		}
	}()

	if err := staging.Chmod(sourceInfo.Mode().Perm()); err != nil {
		// Some removable-drive filesystems do not support Unix permissions.
		// The CreateTemp default is still safe, so permission preservation is
		// best-effort rather than a reason to discard a completed download.
		slog.Debug("preserve completed download permissions", "path", stagingPath, "error", err)
	}
	if _, err := io.Copy(staging, &contextReader{ctx: ctx, reader: source}); err != nil {
		return fmt.Errorf("copy completed download: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := staging.Sync(); err != nil {
		return fmt.Errorf("sync destination staging file: %w", err)
	}
	if err := staging.Close(); err != nil {
		return fmt.Errorf("close destination staging file: %w", err)
	}
	stagingClosed = true
	if err := source.Close(); err != nil {
		return fmt.Errorf("close completed download: %w", err)
	}
	sourceClosed = true

	if err := os.Rename(stagingPath, outputPath); err != nil {
		return fmt.Errorf("commit destination staging file: %w", err)
	}
	committed = true

	if err := os.Remove(sourcePath); err != nil && !errors.Is(err, os.ErrNotExist) {
		slog.Warn("remove source temporary file after cross-device copy", "path", sourcePath, "error", err)
	}

	return nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (reader *contextReader) Read(buffer []byte) (int, error) {
	select {
	case <-reader.ctx.Done():
		return 0, reader.ctx.Err()
	default:
		return reader.reader.Read(buffer)
	}
}
