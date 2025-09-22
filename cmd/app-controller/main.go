// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package main

import (
	"os"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/base"
	// Import app controller packages to trigger init() functions.
	_ "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/app/demo"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/external"
)

func main() {
	err := external.RunControllers("app", base.GetAllControllers)
	if err != nil {
		os.Exit(1)
	}
}
