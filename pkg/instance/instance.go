// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package instance

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
)

var Suffix = sync.OnceValue(func() string {
	instance := os.Getenv("RDD_INSTANCE")
	if instance == "" {
		return "2"
	}
	return instance
})

// Index returns unique number for the control plane instance as long as the
// instance suffix is numeric and in the range from 1 to 99. Otherwise, it is
// computed based on a checksum of the suffix, and may have collisions.
// This index can be used to select unique port numbers per control plane
// (for the apiserver, or for etcd, etc).
var Index = sync.OnceValue(func() int {
	if i, err := strconv.Atoi(Suffix()); err == nil && 0 < i && i < 100 {
		return i
	}
	sum := 0
	for _, ch := range Suffix() {
		sum += int(ch)
	}
	return 100 + (sum % 100)
})

var Name = sync.OnceValue(func() string {
	return "rancher-desktop-" + Suffix()
})

var Dir = sync.OnceValue(func() string {
	home, err := os.UserHomeDir()
	if err != nil {
		panic(fmt.Errorf("could not get home directory: %w", err))
	}
	switch runtime.GOOS {
	case "windows":
		return filepath.Join(home, "AppData", "Local", Name())
	case "linux":
		return filepath.Join(home, ".local", "share", Name())
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", Name())
	default:
		panic(fmt.Sprintf("platform %s not supported", runtime.GOOS))
	}
})

var ArgsFile = sync.OnceValue(func() string {
	return filepath.Join(Dir(), "args.json")
})

var KubeConfig = sync.OnceValue(func() string {
	return filepath.Join(Dir(), "kubeconfig.yaml")
})

var PIDFile = sync.OnceValue(func() string {
	return filepath.Join(Dir(), "rdd.pid")
})

var TLSDir = sync.OnceValue(func() string {
	return filepath.Join(Dir(), "tls")
})

// PathDir returns the path directory for this instance (e.g., ~/.rd2).
// This is distinct from Dir() which returns the service directory.
// See docs/design/cmd_service.md for directory documentation.
var PathDir = sync.OnceValue(func() string {
	home, err := os.UserHomeDir()
	if err != nil {
		panic(fmt.Errorf("could not get home directory: %w", err))
	}
	return filepath.Join(home, ".rd"+Suffix())
})

// LimaHome returns the LIMA_HOME directory for this instance (e.g., ~/.rd2/lima).
// Lima uses this directory instead of the service directory because of socket name
// length constraints.
var LimaHome = sync.OnceValue(func() string {
	return filepath.Join(PathDir(), "lima")
})
