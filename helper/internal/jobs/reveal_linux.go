//go:build linux

package jobs

import (
	"fmt"
	"os/exec"
	"path/filepath"
)

func revealFile(path string) error {
	if err := exec.Command("xdg-open", filepath.Dir(path)).Start(); err != nil {
		return fmt.Errorf("open download directory: %w", err)
	}
	return nil
}
