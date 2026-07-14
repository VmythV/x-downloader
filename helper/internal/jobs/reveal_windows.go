//go:build windows

package jobs

import (
	"fmt"
	"os/exec"
)

func revealFile(path string) error {
	if err := exec.Command("explorer", "/select,", path).Start(); err != nil {
		return fmt.Errorf("reveal downloaded file: %w", err)
	}
	return nil
}
