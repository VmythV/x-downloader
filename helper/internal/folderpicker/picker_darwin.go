//go:build darwin

package folderpicker

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

func pickNative(ctx context.Context) (string, error) {
	command := exec.CommandContext(ctx, "osascript", "-e", `POSIX path of (choose folder with prompt "选择 X Downloader 下载目录")`)
	output, err := command.CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(output))
		if strings.Contains(message, "-128") || strings.Contains(strings.ToLower(message), "user canceled") {
			return "", ErrCancelled
		}
		return "", fmt.Errorf("open macOS directory picker: %w", err)
	}
	return strings.TrimSpace(string(output)), nil
}
