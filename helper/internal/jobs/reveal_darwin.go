//go:build darwin

package jobs

import (
	"fmt"
	"os/exec"
)

func revealFile(path string) error {
	if err := exec.Command("open", "-R", path).Start(); err != nil {
		return fmt.Errorf("reveal downloaded file: %w", err)
	}
	return nil
}
