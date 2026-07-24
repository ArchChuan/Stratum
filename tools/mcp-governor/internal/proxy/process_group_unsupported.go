//go:build !linux

package proxy

import (
	"errors"
	"os/exec"
	"syscall"
)

var errOwnedProcessGroupsUnsupported = errors.New("owned process groups require Linux")

func configureOwnedProcess(*exec.Cmd) error { return errOwnedProcessGroupsUnsupported }

func signalOwnedProcessGroup(*exec.Cmd, syscall.Signal) error {
	return errOwnedProcessGroupsUnsupported
}

func ownedProcessGroupExists(*exec.Cmd) bool { return false }
