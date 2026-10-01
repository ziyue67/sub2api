//go:build !linux

package reauthruntime

import "os/exec"

func configureProcess(cmd *exec.Cmd) {}
