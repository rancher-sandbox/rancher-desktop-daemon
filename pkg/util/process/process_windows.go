// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

// Package process provides cross-platform process utilities.
package process

import (
	"fmt"
	"os/exec"
	"time"

	"golang.org/x/sys/windows"
)

// SetGroup configures the command to run in its own process group.
// On Windows, CREATE_NEW_PROCESS_GROUP allows GenerateConsoleCtrlEvent to
// target only the child process (using its PID as the group ID) without
// affecting the parent process.
func SetGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &windows.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP}
}

// Interrupt sends a graceful shutdown signal to the process with the given PID.
// On Windows, this uses GenerateConsoleCtrlEvent with CTRL_BREAK_EVENT targeted
// at the process group (requires CREATE_NEW_PROCESS_GROUP via SetGroup).
func Interrupt(pid int) error {
	return windows.GenerateConsoleCtrlEvent(windows.CTRL_BREAK_EVENT, uint32(pid))
}

// Kill terminates the process with the given PID.
func Kill(pid int) error {
	hProcess, err := windows.OpenProcess(
		windows.PROCESS_TERMINATE|windows.SYNCHRONIZE,
		false,
		uint32(pid))
	if err != nil {
		return fmt.Errorf("failed to open process %d: %w", pid, err)
	}
	defer func() {
		_ = windows.CloseHandle(hProcess)
	}()
	if err := windows.TerminateProcess(hProcess, 1); err != nil {
		return fmt.Errorf("failed to terminate process %d: %w", pid, err)
	}
	_, err = windows.WaitForSingleObject(hProcess, uint32(10*time.Second/time.Millisecond))
	if err != nil {
		return fmt.Errorf("timed out waiting for process %d to terminate: %w", pid, err)
	}

	return nil
}
