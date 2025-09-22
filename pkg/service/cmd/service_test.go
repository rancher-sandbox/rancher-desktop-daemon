// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package service

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/instance"
	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"
)

func TestCreateWithSecurePortAllocation(t *testing.T) {
	tempDir := t.TempDir()

	// Set up a test instance
	originalName := os.Getenv("RDD_INSTANCE")
	testInstanceName := "test-secure-port-1"
	t.Setenv("RDD_INSTANCE", testInstanceName)
	defer func() {
		if originalName != "" {
			t.Setenv("RDD_INSTANCE", originalName)
		} else {
			os.Unsetenv("RDD_INSTANCE")
		}
	}()

	// Override the instance directory to use our temp directory
	instanceDir := filepath.Join(tempDir, testInstanceName)
	originalInstanceDir := os.Getenv("RDD_INSTANCE_DIR")
	t.Setenv("RDD_INSTANCE_DIR", instanceDir)
	defer func() {
		if originalInstanceDir != "" {
			t.Setenv("RDD_INSTANCE_DIR", originalInstanceDir)
		} else {
			os.Unsetenv("RDD_INSTANCE")
		}
	}()

	// Clean up any existing instance
	if Exists() {
		assert.NilError(t, Delete(), "Failed to delete existing instances")
	}

	// Test the Create function
	err := Create(t.Context(), []string{"--test-arg", "value"})
	assert.NilError(t, err, "Create failed")

	// Verify that the args file was created
	argsFile := instance.ArgsFile()
	_, err = os.Stat(argsFile)
	assert.Assert(t, !os.IsNotExist(err), "Args file was not created")

	// Read and verify the args file
	data, err := os.ReadFile(argsFile)
	assert.NilError(t, err, "Failed to read args file")

	var args []string
	err = json.Unmarshal(data, &args)
	assert.NilError(t, err, "Failed to unmarshal args")

	// Check that secure-port argument is present
	securePortFound := false
	var securePortValue string
	for i, arg := range args {
		if arg == "--secure-port" && i+1 < len(args) {
			securePortFound = true
			securePortValue = args[i+1]
			break
		}
	}

	assert.Assert(t, securePortFound, "--secure-port argument not found in args: %v", args)

	// Verify that the secure port is a valid port number
	port, err := strconv.Atoi(securePortValue)
	assert.NilError(t, err, "Invalid secure port value: %s", securePortValue)

	assert.Assert(t, port > 0 && port <= 65535, "Secure port out of valid range: %d", port)

	// Verify that other expected arguments are present
	expectedArgs := []string{"--test-arg", "value"}
	for _, expectedArg := range expectedArgs {
		assert.Assert(t, cmp.Contains(args, expectedArg), "Expected argument not found")
	}

	t.Logf("Successfully created instance with secure port: %d", port)
}

func TestCreateWithOccupiedSecurePort(t *testing.T) {
	tempDir := t.TempDir()

	// Set up a test instance
	originalName := os.Getenv("RDD_INSTANCE")
	testInstanceName := "test-occupied-port-2"
	t.Setenv("RDD_INSTANCE", testInstanceName)
	defer func() {
		if originalName != "" {
			t.Setenv("RDD_INSTANCE", originalName)
		} else {
			os.Unsetenv("RDD_INSTANCE")
		}
	}()

	// Override the instance directory to use our temp directory
	instanceDir := filepath.Join(tempDir, testInstanceName)
	originalInstanceDir := os.Getenv("RDD_INSTANCE_DIR")
	t.Setenv("RDD_INSTANCE_DIR", instanceDir)
	defer func() {
		if originalInstanceDir != "" {
			t.Setenv("RDD_INSTANCE_DIR", originalInstanceDir)
		} else {
			os.Unsetenv("RDD_INSTANCE_DIR")
		}
	}()

	// Clean up any existing instance
	if Exists() {
		assert.NilError(t, Delete(), "Failed to delete existing instance")
	}

	// Calculate the expected secure port for this instance
	expectedSecurePort := 6443 + instance.Index()

	// Occupy the expected secure port
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", fmt.Sprintf(":%d", expectedSecurePort))
	if err != nil {
		t.Skipf("Could not bind to expected secure port %d: %v", expectedSecurePort, err)
	}
	defer listener.Close()

	// Test the Create function - it should find an alternative port
	err = Create(t.Context(), []string{"--test-arg", "value"})
	assert.NilError(t, err, "Create failed")

	// Read and verify the args file
	data, err := os.ReadFile(instance.ArgsFile())
	assert.NilError(t, err, "Failed to read args file")

	var args []string
	err = json.Unmarshal(data, &args)
	assert.NilError(t, err, "Failed to unmarshal args")

	// Check that secure-port argument is present and different from expected
	securePortFound := false
	var actualSecurePort int
	for i, arg := range args {
		if arg == "--secure-port" && i+1 < len(args) {
			securePortFound = true
			actualSecurePort, err = strconv.Atoi(args[i+1])
			assert.NilError(t, err, "Invalid secure port value: %s", args[i+1])
			break
		}
	}

	assert.Assert(t, securePortFound, "--secure-port argument not found in args: %v", args)

	// Verify that the allocated port is different from the occupied one
	assert.Assert(t, actualSecurePort != expectedSecurePort, "Expected different port than occupied port %d, got %d", expectedSecurePort, actualSecurePort)

	t.Logf("Successfully allocated alternative secure port %d instead of occupied port %d", actualSecurePort, expectedSecurePort)
}
