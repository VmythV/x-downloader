package folderpicker

import (
	"context"
	"errors"
)

var ErrCancelled = errors.New("directory selection cancelled")

type Picker interface {
	Pick(context.Context) (string, error)
}

type PickerFunc func(context.Context) (string, error)

func (function PickerFunc) Pick(ctx context.Context) (string, error) {
	return function(ctx)
}

type Native struct{}

func (Native) Pick(ctx context.Context) (string, error) {
	return pickNative(ctx)
}
