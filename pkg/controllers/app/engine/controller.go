// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

// Package engine registers the engine controller. The engine controller mirrors
// Docker container engine state (containers, images, volumes) into Kubernetes
// resources and forwards K8s deletions back to Docker.
package engine

import (
	ctrl "sigs.k8s.io/controller-runtime"

	appv1alpha1 "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/app/v1alpha1"
	containersv1alpha1 "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/containers/v1alpha1"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/app/engine/controllers"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/base"
)

func init() {
	base.RegisterController(newController())
}

const (
	controllerName = "engine"
	apiGroup       = "engine"
)

type controller struct{}

func newController() base.Controller {
	return &controller{}
}

var _ base.Controller = &controller{}

func (c *controller) GetName() string {
	return controllerName
}

func (c *controller) GetAPIGroup() string {
	return apiGroup
}

func (c *controller) GetCRDData() string {
	return ""
}

func (c *controller) RegisterWithManager(mgr ctrl.Manager) error {
	if err := appv1alpha1.AddToScheme(mgr.GetScheme()); err != nil {
		return err
	}
	if err := containersv1alpha1.AddToScheme(mgr.GetScheme()); err != nil {
		return err
	}

	return (&controllers.EngineReconciler{
		Client: mgr.GetClient(),
	}).SetupWithManager(mgr)
}
