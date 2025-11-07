// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package namespace

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/base"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/builtin/namespace/controllers"
)

// ControllerName is the name of this controller.
const ControllerName = "namespace"

// APIGroup is the API group this controller belongs to.
const APIGroup = "builtin"

func init() {
	base.RegisterController(NewController())
}

// Controller implements the base.Controller interface for namespace lifecycle management.
type Controller struct{}

// Verify that Controller implements base.Controller interface.
var _ base.Controller = &Controller{}

// NewController creates a new Controller instance.
func NewController() *Controller {
	return &Controller{}
}

// GetName returns the controller name.
func (c *Controller) GetName() string {
	return ControllerName
}

// GetAPIGroup returns the API group this controller belongs to.
func (c *Controller) GetAPIGroup() string {
	return APIGroup
}

// GetCRDData returns empty string since namespace is a built-in Kubernetes resource.
func (c *Controller) GetCRDData() string {
	return ""
}

// RegisterWithManager implements the complete controller registration.
func (c *Controller) RegisterWithManager(mgr ctrl.Manager) error {
	klog.InfoS("Setting up namespace controller watch", "controller", ControllerName)

	// Register the controller
	// Note: Resource discovery happens dynamically during each reconciliation
	// to ensure we always have the most up-to-date list of namespaced resources
	err := ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Namespace{}).
		Complete(&controllers.NamespaceReconciler{
			Client:  mgr.GetClient(),
			Scheme:  mgr.GetScheme(),
			Manager: mgr,
		})
	if err != nil {
		klog.ErrorS(err, "Failed to setup namespace controller", "controller", ControllerName)
		return err
	}
	klog.InfoS("Namespace controller watch setup complete", "controller", ControllerName)
	return nil
}
