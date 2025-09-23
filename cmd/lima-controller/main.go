// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package main

import (
	// Import rdd controller packages to trigger init() functions.
	_ "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/lima/limavm"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/external"
)

func main() {
	external.RunControllers("lima")
}
