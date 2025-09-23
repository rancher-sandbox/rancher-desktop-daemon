//go:build unix

package service

import (
	"os/exec"

	"golang.org/x/sys/unix"
)

func killProcess(pid int) error {
	return unix.Kill(pid, unix.SIGTERM)
}

func setCommandGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &unix.SysProcAttr{Setpgid: true}
}
