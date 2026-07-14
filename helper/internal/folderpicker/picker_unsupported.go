//go:build !darwin && !windows && !linux

package folderpicker

import (
	"context"
	"errors"
)

func pickNative(context.Context) (string, error) {
	return "", errors.New("native directory picker is not supported on this platform")
}
