// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package limavm

import (
	"context"
	"errors"
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/lima/v1alpha1"
)

// validateLimaVMUniqueName validates that the LimaVM name is unique across all namespaces.
// This is critical because LimaVM names correspond to actual VM instances on the host system,
// which must be unique.
func validateLimaVMUniqueName(ctx context.Context, c client.Client, limavm *v1alpha1.LimaVM) error {
	// List all LimaVMs across all namespaces
	limavmList := &v1alpha1.LimaVMList{}
	if err := c.List(ctx, limavmList, &client.ListOptions{}); err != nil {
		return fmt.Errorf("failed to list LimaVMs for uniqueness check: %w", err)
	}

	// Check for name conflicts in other namespaces
	for _, existingVM := range limavmList.Items {
		// Skip if it's an update to the existing resource (or a different name)
		if existingVM.Namespace == limavm.Namespace {
			continue
		}

		// Check if an instance with the same name exists in a different namespace
		if existingVM.Name == limavm.Name {
			return fmt.Errorf("LimaVM name %q is already used in namespace %q; LimaVM names must be unique across all namespaces",
				limavm.Name, existingVM.Namespace)
		}
	}

	return nil
}

// validateLimaVM validates a complete LimaVM object and returns warnings.
func validateLimaVM(ctx context.Context, c client.Client, limavm *v1alpha1.LimaVM) ([]string, error) {
	if limavm == nil {
		return nil, errors.New("limavm object cannot be nil")
	}

	var warnings []string
	if err := validateLimaVMUniqueName(ctx, c, limavm); err != nil {
		return warnings, err
	}

	return warnings, nil
}
