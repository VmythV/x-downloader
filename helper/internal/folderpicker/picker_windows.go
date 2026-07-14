//go:build windows

package folderpicker

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

func pickNative(ctx context.Context) (string, error) {
	script := `Add-Type -AssemblyName System.Windows.Forms; $dialog = New-Object System.Windows.Forms.FolderBrowserDialog; $dialog.Description = 'Select X Downloader download directory'; if ($dialog.ShowDialog() -eq [System.Windows.Forms.DialogResult]::OK) { [Console]::OutputEncoding = [System.Text.Encoding]::UTF8; Write-Output $dialog.SelectedPath } else { exit 2 }`
	output, err := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-Command", script).CombinedOutput()
	if exitError, ok := err.(*exec.ExitError); ok && exitError.ExitCode() == 2 {
		return "", ErrCancelled
	}
	if err != nil {
		return "", fmt.Errorf("open Windows directory picker: %w", err)
	}
	return strings.TrimSpace(string(output)), nil
}
