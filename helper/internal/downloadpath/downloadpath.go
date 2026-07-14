package downloadpath

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func Normalize(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", errors.New("download directory must not be empty")
	}
	cleaned := filepath.Clean(path)
	if !filepath.IsAbs(cleaned) {
		return "", errors.New("download directory must be absolute")
	}
	return cleaned, nil
}

func Prepare(path string) (string, error) {
	cleaned, err := Normalize(path)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(cleaned, 0o755); err != nil {
		return "", fmt.Errorf("create download directory: %w", err)
	}
	file, err := os.CreateTemp(cleaned, ".write-check-*")
	if err != nil {
		return "", fmt.Errorf("download directory is not writable: %w", err)
	}
	name := file.Name()
	if err := file.Close(); err != nil {
		_ = os.Remove(name)
		return "", fmt.Errorf("check download directory: %w", err)
	}
	if err := os.Remove(name); err != nil {
		return "", fmt.Errorf("clean download directory check: %w", err)
	}
	return cleaned, nil
}

func Writable(path string) bool {
	cleaned, err := Normalize(path)
	if err != nil {
		return false
	}
	file, err := os.CreateTemp(cleaned, ".write-check-*")
	if err != nil {
		return false
	}
	name := file.Name()
	if err := file.Close(); err != nil {
		_ = os.Remove(name)
		return false
	}
	return os.Remove(name) == nil
}
