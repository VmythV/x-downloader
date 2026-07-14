//go:build !darwin && !linux && !windows

package jobs

import "errors"

func revealFile(_ string) error {
	return errors.New("revealing downloaded files is not supported on this platform")
}
