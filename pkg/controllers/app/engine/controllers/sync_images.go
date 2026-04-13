// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	mobyimage "github.com/moby/moby/api/types/image"
	dockerclient "github.com/moby/moby/client"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	containersv1alpha1 "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/containers/v1alpha1"
	containersv1alpha1apply "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/containers/v1alpha1/applyconfiguration/containers/v1alpha1"
)

// sanitizeKubernetesObjectName replaces characters not allowed in
// Kubernetes object names.
func sanitizeKubernetesObjectName(input string) string {
	return strings.NewReplacer("/", "-", ":", ".").Replace(input)
}

// imageMirrorNames derives the deterministic `Image` mirror names that
// correspond to a Docker image ID and its RepoTags. A tagged image gets
// one mirror per tag (name = sanitized-id + "-" + sha256(tag)); a
// dangling image gets a single mirror named after the sanitized ID.
// Sharing this helper between applyImageFromInspect and syncAllImages
// lets the stale-mirror sweep seed activeNames from the list response
// alone, so a transient Inspect failure cannot classify existing
// mirrors as stale.
func imageMirrorNames(id string, repoTags []string) []string {
	if len(repoTags) == 0 {
		return []string{sanitizeKubernetesObjectName(id)}
	}
	names := make([]string, 0, len(repoTags))
	for _, tag := range repoTags {
		names = append(names, fmt.Sprintf("%s-%x",
			sanitizeKubernetesObjectName(id),
			sha256.Sum256([]byte(tag))))
	}
	return names
}

// syncAllImages lists all Docker images and creates/updates `Image`
// mirrors, then removes stale ones.
//
// Images are Inspected sequentially for the same reason as
// syncAllContainers: one-shot at startup, typical dev machines have
// tens of images, and we need fields that only Inspect exposes
// (RepoDigests, full Labels, detailed metadata). Parallelise here if
// startup latency becomes a concern.
func (w *dockerWatcher) syncAllImages(ctx context.Context) error {
	log := logf.FromContext(ctx).WithName("docker-watcher")

	listResult, err := w.cli.ImageList(ctx, dockerclient.ImageListOptions{All: true})
	if err != nil {
		return fmt.Errorf("failed to list images: %w", err)
	}

	// Track which `Image` mirror names we create so we can prune stale ones.
	activeNames := make(map[string]bool)

	// Per-item inspect failures are logged and skipped rather than
	// failing the whole startup (see the matching note in
	// syncAllContainers): a single broken image must not block the
	// watcher from coming up. Structural errors below (k8s list,
	// stale-mirror cleanup) are still fatal.
	//
	// Seed activeNames from the list response before calling Inspect.
	// If Inspect fails transiently the mirror stays in activeNames and
	// is not pruned below — otherwise a flaky Docker daemon would
	// classify a perfectly good Image mirror as stale and delete it.
	var errs []error
	for _, summary := range listResult.Items {
		for _, n := range imageMirrorNames(summary.ID, summary.RepoTags) {
			activeNames[n] = true
		}
		if err := w.syncImageFromSummary(ctx, summary); err != nil {
			log.Error(err, "Skipping image during full sync", "id", summary.ID)
		}
	}

	// Remove stale Image mirrors.
	var imageMirrors containersv1alpha1.ImageList
	if err := w.k8s.List(ctx, &imageMirrors, client.InNamespace(apiNamespace)); err != nil {
		return fmt.Errorf("failed to list Images: %w", err)
	}
	for i := range imageMirrors.Items {
		img := &imageMirrors.Items[i]
		if !activeNames[img.Name] {
			log.V(1).Info("Removing stale Image", "name", img.Name)
			if err := w.removeMirrorResource(ctx, img, img.Name); err != nil {
				errs = append(errs, err)
			}
		}
	}

	return errors.Join(errs...)
}

// syncImageFromSummary creates `Image` mirrors from a Docker image
// summary. NotFound races during fullSync are treated as success; the
// stale Image mirror is pruned later by syncAllImages' remove-stale step.
func (w *dockerWatcher) syncImageFromSummary(ctx context.Context, summary mobyimage.Summary) error {
	result, err := w.cli.ImageInspect(ctx, summary.ID)
	if cerrdefs.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to inspect image %s: %w", summary.ID, err)
	}
	_, err = w.applyImageFromInspect(ctx, result.InspectResponse)
	return err
}

// applyImageFromInspect creates or updates `Image` mirrors from a Docker
// InspectResponse. One Image per tag, plus one for dangling images.
// Returns the mirror names that were created so reconcileImageByID can
// prune mirrors whose tags were removed.
func (w *dockerWatcher) applyImageFromInspect(ctx context.Context, inspect mobyimage.InspectResponse) ([]string, error) {
	names := imageMirrorNames(inspect.ID, inspect.RepoTags)
	var errs []error

	if len(inspect.RepoTags) > 0 {
		for i, tag := range inspect.RepoTags {
			if err := w.applyImage(ctx,
				containersv1alpha1apply.Image(names[i], apiNamespace).
					WithFinalizers(mirrorFinalizer),
				imageStatusFromInspect(inspect).
					WithRepoTag(tag).
					WithNamespace(containerNamespace),
			); err != nil {
				errs = append(errs, err)
			}
		}
	} else {
		// Dangling image (no tags). status.namespace is a required
		// field on the CRD, so set it here too even though it carries
		// no additional information beyond what the tagged branch sets.
		if err := w.applyImage(ctx,
			containersv1alpha1apply.Image(names[0], apiNamespace).
				WithFinalizers(mirrorFinalizer),
			imageStatusFromInspect(inspect).
				WithNamespace(containerNamespace),
		); err != nil {
			errs = append(errs, err)
		}
	}

	return names, errors.Join(errs...)
}

// imageStatusFromInspect builds a fresh ImageStatus apply config from a
// Docker inspect response. Returning a new value per call avoids the
// aliasing trap of a shallow struct copy — slice/map WithX calls on
// one "copy" would otherwise mutate the backing memory shared with
// other callers.
func imageStatusFromInspect(inspect mobyimage.InspectResponse) *containersv1alpha1apply.ImageStatusApplyConfiguration {
	// Sort RepoDigests before applying: the field is atomic under SSA,
	// and Docker does not guarantee a stable order between inspects, so
	// an unsorted apply would mint a new resourceVersion on every sync
	// and trigger a cascade of engine-reconciler reconciles.
	digests := slices.Clone(inspect.RepoDigests)
	sort.Strings(digests)
	statusApply := containersv1alpha1apply.ImageStatus().
		WithID(inspect.ID).
		WithRepoDigests(digests...).
		WithArchitecture(inspect.Architecture).
		WithOS(inspect.Os).
		WithSize(inspect.Size).
		WithLabels(inspect.Config.Labels)

	if t, err := time.Parse(time.RFC3339Nano, inspect.Created); err == nil {
		statusApply.WithCreatedAt(metav1.NewTime(t))
	}
	return statusApply
}

// applyImage creates or updates a single `Image` mirror and its status.
func (w *dockerWatcher) applyImage(
	ctx context.Context,
	image *containersv1alpha1apply.ImageApplyConfiguration,
	status *containersv1alpha1apply.ImageStatusApplyConfiguration,
) error {
	err := w.k8s.Apply(ctx, image,
		client.ForceOwnership, client.FieldOwner(controllerName))
	if err != nil {
		return fmt.Errorf("failed to apply image %s: %w", *image.GetName(), err)
	}

	err = w.k8s.SubResource("status").Apply(ctx,
		containersv1alpha1apply.Image(*image.GetName(), *image.GetNamespace()).
			WithStatus(status),
		client.ForceOwnership, client.FieldOwner(controllerName))
	if err != nil {
		return fmt.Errorf("failed to apply image status %s: %w", *image.GetName(), err)
	}

	return nil
}

// reconcileImageByID re-inspects a Docker image and reconciles every
// `Image` mirror whose status.id matches. Tags still present are
// re-applied; Image mirrors for tags that are no longer present are
// deleted. If the image has been fully removed from Docker, all mirrors
// with that status.id are deleted instead.
//
// This is the path for events that carry an image ID but not the tag
// name — notably Docker's untag events, where the event payload only
// contains the image ID (see handleImageEvent).
func (w *dockerWatcher) reconcileImageByID(ctx context.Context, id string) error {
	result, err := w.cli.ImageInspect(ctx, id)
	if cerrdefs.IsNotFound(err) {
		return w.removeImagesByID(ctx, id)
	}
	if err != nil {
		return fmt.Errorf("failed to inspect image %s: %w", id, err)
	}

	freshNames, applyErr := w.applyImageFromInspect(ctx, result.InspectResponse)
	keep := make(map[string]bool, len(freshNames))
	for _, n := range freshNames {
		keep[n] = true
	}

	var images containersv1alpha1.ImageList
	if err := w.k8s.List(ctx, &images, client.InNamespace(apiNamespace)); err != nil {
		return errors.Join(applyErr, err)
	}
	errs := []error{applyErr}
	for i := range images.Items {
		img := &images.Items[i]
		if img.Status.ID != result.InspectResponse.ID {
			continue
		}
		if keep[img.Name] {
			continue
		}
		if err := w.removeMirrorResource(ctx, img, img.Name); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// removeImagesByID finds and removes all `Image` mirrors for a given Docker image ID.
func (w *dockerWatcher) removeImagesByID(ctx context.Context, id string) error {
	var images containersv1alpha1.ImageList
	if err := w.k8s.List(ctx, &images, client.InNamespace(apiNamespace)); err != nil {
		return err
	}
	var errs []error
	for i := range images.Items {
		if images.Items[i].Status.ID == id {
			if err := w.removeMirrorResource(ctx, &images.Items[i], images.Items[i].Name); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}
