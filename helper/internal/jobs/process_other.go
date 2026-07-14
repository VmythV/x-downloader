//go:build !darwin && !linux

package jobs

import (
	"os/exec"
)

func prepareProcess(_ *exec.Cmd) {}

func signalProcess(command *exec.Cmd) {
	if command.Process != nil {
		_ = command.Process.Kill()
	}
}

func killProcess(command *exec.Cmd) { signalProcess(command) }
