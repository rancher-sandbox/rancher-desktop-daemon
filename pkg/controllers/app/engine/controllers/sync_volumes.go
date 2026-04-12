// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"context"
	"errors"
	"fmt"
	"time"

	mobyvolume "github.com/moby/moby/api/types/volume"
	dockerclient "github.com/moby/moby/client"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	containersv1alpha1 "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/containers/v1alpha1"
	containersv1alpha1apply "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/containers/v1alpha1/applyconfiguration/containers/v1alpha1"
)

// syncContainerNamespace creates the "moby" ContainerNamespace resource.
func (w *dockerWatcher) syncContainerNamespace(ctx context.Context) error {
	applyConfig := containersv1alpha1apply.ContainerNamespace(containerNamespace, apiNamespace).
		WithFinalizers(mirrorFinalizer)

	return w.k8s.Apply(ctx, applyConfig,
		client.ForceOwnership, client.FieldOwner(controllerName))
}

// syncAllVolumes lists all Docker volumes and creates/updates K8s resources,
// then removes stale ones.
func (w *dockerWatcher) syncAllVolumes(ctx context.Context) error {
	log := logf.FromContext(ctx).WithName("docker-watcher")

	volumeList, err := w.cli.VolumeList(ctx, dockerclient.VolumeListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list volumes: %w", err)
	}

	// Track which K8s resource names we create.
	activeNames := make(map[string]bool, len(volumeList.Items))

	var errs []error
	for _, v := range volumeList.Items {
		k8sName := sanitizeKubernetesObjectName(v.Name)
		activeNames[k8sName] = true
		if err := w.applyVolume(ctx, v); err != nil {
			errs = append(errs, err)
		}
	}

	// Remove stale K8s volumes.
	var k8sVolumes containersv1alpha1.VolumeList
	if err := w.k8s.List(ctx, &k8sVolumes, client.InNamespace(apiNamespace)); err != nil {
		return fmt.Errorf("failed to list K8s volumes: %w", err)
	}
	for i := range k8sVolumes.Items {
		vol := &k8sVolumes.Items[i]
		if !activeNames[vol.Name] {
			log.V(1).Info("Removing stale volume", "name", vol.Name)
			if err := w.removeMirrorResource(ctx, vol, vol.Name); err != nil {
				errs = append(errs, err)
			}
		}
	}

	return errors.Join(errs...)
}

// syncVolume looks up a single volume by name and creates/updates the K8s resource.
func (w *dockerWatcher) syncVolume(ctx context.Context, name string) error {
	result, err := w.cli.VolumeInspect(ctx, name, dockerclient.VolumeInspectOptions{})
	if err != nil {
		return fmt.Errorf("failed to inspect volume %s: %w", name, err)
	}
	return w.applyVolume(ctx, result.Volume)
}

// applyVolume creates or updates a Volume resource from a Docker volume.
func (w *dockerWatcher) applyVolume(ctx context.Context, vol mobyvolume.Volume) error {
	k8sName := sanitizeKubernetesObjectName(vol.Name)

	applyConfig := containersv1alpha1apply.Volume(k8sName, apiNamespace).
		WithFinalizers(mirrorFinalizer)

	err := w.k8s.Apply(ctx, applyConfig,
		client.ForceOwnership, client.FieldOwner(controllerName))
	if err != nil {
		return fmt.Errorf("failed to apply volume %s: %w", k8sName, err)
	}

	statusApply := containersv1alpha1apply.VolumeStatus().
		WithName(vol.Name).
		WithNamespace(containerNamespace).
		WithDriver(vol.Driver).
		WithLabels(vol.Labels).
		WithOptions(vol.Options).
		WithMountPoint(vol.Mountpoint).
		WithScope(vol.Scope)

	if t, err := time.Parse(time.RFC3339Nano, vol.CreatedAt); err == nil {
		statusApply.WithCreatedAt(metav1.NewTime(t))
	}

	err = w.k8s.SubResource("status").Apply(ctx,
		containersv1alpha1apply.Volume(k8sName, apiNamespace).
			WithStatus(statusApply),
		client.ForceOwnership, client.FieldOwner(controllerName))
	if err != nil {
		return fmt.Errorf("failed to apply volume status %s: %w", k8sName, err)
	}

	return nil
}
