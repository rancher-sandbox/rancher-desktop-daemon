// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

// Package base provides shared utilities for RDD controllers.
package base

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// FinalizerHelper provides common finalizer functionality for controllers.
type FinalizerHelper struct {
	Client        client.Client
	Scheme        *runtime.Scheme
	FinalizerName string
	Enabled       bool
}

// NewFinalizerHelper creates a new finalizer helper with the given client, scheme, and finalizer name.
// If finalizerName is empty, the helper will be disabled and all operations will be no-ops.
func NewFinalizerHelper(client client.Client, scheme *runtime.Scheme, finalizerName string) *FinalizerHelper {
	return &FinalizerHelper{
		Client:        client,
		Scheme:        scheme,
		FinalizerName: finalizerName,
		Enabled:       finalizerName != "",
	}
}

// NewNoOpFinalizerHelper creates a disabled finalizer helper that performs no operations.
// This is useful for controllers that don't need finalizer functionality.
func NewNoOpFinalizerHelper() *FinalizerHelper {
	return &FinalizerHelper{
		Enabled: false,
	}
}

// EnsureFinalizer adds the finalizer to the object if it's not already present.
// Returns true if the finalizer was added and the object has been updated.
// When true is returned, controllers should return immediately to allow re-reconciliation
// with the updated object (avoids stale resourceVersion conflicts).
// If the helper is disabled, this is a no-op that returns false, nil.
func (h *FinalizerHelper) EnsureFinalizer(ctx context.Context, obj client.Object) (bool, error) {
	if !h.Enabled {
		return false, nil
	}

	if !controllerutil.ContainsFinalizer(obj, h.FinalizerName) {
		controllerutil.AddFinalizer(obj, h.FinalizerName)
		if err := h.Client.Update(ctx, obj); err != nil {
			return false, fmt.Errorf("failed to add finalizer: %w", err)
		}
		return true, nil
	}
	return false, nil
}

// IsBeingDeleted checks if the object is being deleted (has deletion timestamp).
func (h *FinalizerHelper) IsBeingDeleted(obj client.Object) bool {
	return obj.GetDeletionTimestamp() != nil
}

// RemoveFinalizer removes the finalizer from the object and updates it.
// If the helper is disabled, this is a no-op.
func (h *FinalizerHelper) RemoveFinalizer(ctx context.Context, obj client.Object) error {
	if !h.Enabled {
		return nil
	}

	controllerutil.RemoveFinalizer(obj, h.FinalizerName)
	if err := h.Client.Update(ctx, obj); err != nil {
		return fmt.Errorf("failed to remove finalizer: %w", err)
	}
	return nil
}

// CleanupOptions provides configuration for resource cleanup.
type CleanupOptions struct {
	// ResourceType is the type of resource to clean up (e.g., &corev1.ConfigMap{})
	ResourceType client.ObjectList
	// Namespace to search in (empty for cluster-scoped resources)
	Namespace string
	// LabelSelector to find resources to clean up
	LabelSelector map[string]string
	// OwnerVerifier is an optional function to verify ownership before deletion
	OwnerVerifier func(resource client.Object, owner client.Object) bool
}

// CleanupOwnedResources finds and deletes all resources owned by the given object.
// If the helper is disabled, this is a no-op.
func (h *FinalizerHelper) CleanupOwnedResources(ctx context.Context, owner client.Object, opts CleanupOptions) error {
	if !h.Enabled {
		return nil
	}

	logger := log.FromContext(ctx)

	// List resources using label selector
	listOpts := []client.ListOption{
		client.MatchingLabels(opts.LabelSelector),
	}
	if opts.Namespace != "" {
		listOpts = append(listOpts, client.InNamespace(opts.Namespace))
	}

	if err := h.Client.List(ctx, opts.ResourceType, listOpts...); err != nil {
		return fmt.Errorf("failed to list resources for cleanup: %w", err)
	}

	items, err := extractListItems(opts.ResourceType)
	if err != nil {
		return fmt.Errorf("failed to extract items from resource list: %w", err)
	}

	// Delete each owned resource
	for _, obj := range items {
		// Verify ownership if verifier is provided
		if opts.OwnerVerifier != nil && !opts.OwnerVerifier(obj, owner) {
			logger.Info("Skipping resource not owned by this controller",
				"resourceType", fmt.Sprintf("%T", obj),
				"resourceName", obj.GetName())
			continue
		}

		logger.Info("Deleting resource during cleanup",
			"resourceType", fmt.Sprintf("%T", obj),
			"resourceNamespace", obj.GetNamespace(),
			"resourceName", obj.GetName())

		if err := h.Client.Delete(ctx, obj); err != nil && client.IgnoreNotFound(err) != nil {
			return fmt.Errorf("failed to delete resource during cleanup: %w", err)
		}
	}

	return nil
}

// CreateOwnerVerifier creates a standard owner reference verifier for the given API version and kind.
func CreateOwnerVerifier(expectedAPIVersion, expectedKind string) func(resource client.Object, owner client.Object) bool {
	return func(resource client.Object, owner client.Object) bool {
		for _, ownerRef := range resource.GetOwnerReferences() {
			if ownerRef.UID == owner.GetUID() &&
				ownerRef.Kind == expectedKind &&
				ownerRef.APIVersion == expectedAPIVersion {
				return true
			}
		}
		return false
	}
}

// extractListItems extracts individual items from a client.ObjectList.
func extractListItems(list client.ObjectList) ([]client.Object, error) {
	// Use reflection to handle different list types generically
	v := reflect.ValueOf(list)
	if v.Kind() != reflect.Ptr {
		return nil, errors.New("list must be a pointer")
	}

	v = v.Elem()
	if v.Kind() != reflect.Struct {
		return nil, errors.New("list must be a struct")
	}

	// Look for Items field
	itemsField := v.FieldByName("Items")
	if !itemsField.IsValid() {
		return nil, errors.New("list does not have Items field")
	}

	if itemsField.Kind() != reflect.Slice {
		return nil, errors.New("items field is not a slice")
	}

	// Extract items
	items := make([]client.Object, itemsField.Len())
	for i := range itemsField.Len() {
		item := itemsField.Index(i)
		// Get address of the item to make it a pointer
		if item.CanAddr() {
			obj, ok := item.Addr().Interface().(client.Object)
			if !ok {
				return nil, fmt.Errorf("item at index %d is not a client.Object", i)
			}
			items[i] = obj
		} else {
			return nil, fmt.Errorf("cannot get address of item at index %d", i)
		}
	}

	return items, nil
}
