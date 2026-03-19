// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/lima-vm/lima/v2/pkg/limatype/dirnames"
	"github.com/lima-vm/lima/v2/pkg/limatype/filenames"
)

// ensureSSHKeys generates Lima's SSH keypair if it doesn't exist.
// Lima's DefaultPubKeys uses cygpath to convert the key path to MSYS2 format
// (/c/...), which only works with MSYS2's ssh-keygen. Windows OpenSSH's
// ssh-keygen doesn't understand MSYS2 paths and fails with "No such file".
// By pre-generating the key with a native Windows path, Lima finds the
// existing key and skips its broken keygen path.
func ensureSSHKeys() error {
	configDir, err := dirnames.LimaConfigDir()
	if err != nil {
		return err
	}
	privPath := filepath.Join(configDir, filenames.UserPrivateKey)
	if _, err := os.Stat(privPath); err == nil {
		return nil
	}
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		return fmt.Errorf("could not create %q: %w", configDir, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ssh-keygen", "-t", "ed25519", "-q", "-N", "",
		"-C", "lima", "-f", privPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to generate SSH key: %s: %w", out, err)
	}
	return nil
}
