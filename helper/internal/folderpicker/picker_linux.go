//go:build linux

package folderpicker

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

func pickNative(ctx context.Context) (string, error) {
	if _, err := exec.LookPath("zenity"); err == nil {
		return runLinuxPicker(ctx, "zenity", "--file-selection", "--directory", "--title=选择 X Downloader 下载目录")
	}
	if _, err := exec.LookPath("kdialog"); err == nil {
		return runLinuxPicker(ctx, "kdialog", "--getexistingdirectory", ".", "--title", "选择 X Downloader 下载目录")
	}
	return "", errors.New("directory picker requires zenity or kdialog")
}

func runLinuxPicker(ctx context.Context, name string, arguments ...string) (string, error) {
	output, err := exec.CommandContext(ctx, name, arguments...).CombinedOutput()
	if exitError, ok := err.(*exec.ExitError); ok && exitError.ExitCode() == 1 {
		return "", ErrCancelled
	}
	if err != nil {
		return "", fmt.Errorf("open Linux directory picker: %w", err)
	}
	return strings.TrimSpace(string(output)), nil
}
