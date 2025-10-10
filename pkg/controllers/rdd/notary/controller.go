// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package notary

import (
	"context"
	_ "embed"
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlwebhookadmission "sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/rdd/v1alpha1"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/base"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/rdd/notary/controllers"
)

func init() {
	base.RegisterController(NewController())
}

// ControllerName is the name of this controller.
const ControllerName = "notary"

// APIGroup is the API group this controller belongs to.
const APIGroup = "rdd"

// Webhook configuration constants.
const (
	// webhookName is the name used for the webhook configuration.
	webhookName = "notary.rdd.rancherdesktop.io"
	// validatorConfigName is the name of the ValidatingWebhookConfiguration.
	validatorConfigName = "notary-validator"
)

//go:embed crd.yaml
var notaryCRD string

// Controller implements the base.Controller interface for notary.
type Controller struct {
	webhookPort    int                  // The actual webhook port allocated by SharedControllerManager
	webhookManager *base.WebhookManager // WebhookManager for parallel setup
}

// Verify that Controller implements base.Controller and base.WebhookController interfaces.
var (
	_ base.Controller        = &Controller{}
	_ base.WebhookController = &Controller{}
)

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

// SetWebhookPort sets the webhook port allocated by SharedControllerManager.
func (c *Controller) SetWebhookPort(port int) {
	c.webhookPort = port
}

// GetWebhookServiceName returns the DNS service name for webhook certificates.
func (c *Controller) GetWebhookServiceName() string {
	return "notary-webhook"
}

// GetWebhookManager returns the WebhookManager for parallel setup.
func (c *Controller) GetWebhookManager() *base.WebhookManager {
	return c.webhookManager
}

// GetCRDData returns the embedded CRD YAML data.
func (c *Controller) GetCRDData() string {
	return notaryCRD
}

// setupReconciler sets up the NotaryReconciler with the manager.
func (c *Controller) setupReconciler(mgr ctrl.Manager) error {
	return (&controllers.NotaryReconciler{
		Client:          mgr.GetClient(),
		Scheme:          mgr.GetScheme(),
		Recorder:        mgr.GetEventRecorderFor("notary-controller"),
		FinalizerHelper: base.NewFinalizerHelper(mgr.GetClient(), mgr.GetScheme(), controllers.FinalizerName),
	}).SetupWithManager(mgr)
}

// setupWebhookWithRuntimeConfig sets up webhook with shared certificate configuration.
func (c *Controller) setupWebhookWithRuntimeConfig(mgr ctrl.Manager) error {
	webhookConfig := base.WebhookConfig{
		Name:        validatorConfigName,
		WebhookName: webhookName,
		WebhookPath: base.GenerateValidatingWebhookPath(
			v1alpha1.GroupVersion.Group,
			v1alpha1.GroupVersion.Version,
			ControllerName,
		),
		APIGroup:    v1alpha1.GroupVersion.Group,
		APIVersion:  v1alpha1.GroupVersion.Version,
		Resource:    "notaries",
		WebhookPort: c.webhookPort,
	}

	manager, err := base.SetupWebhookForResource(
		mgr,
		&v1alpha1.Notary{},
		&NotaryValidator{},
		webhookConfig,
	)
	if err != nil {
		return err
	}

	c.webhookManager = manager
	return nil
}

// RegisterWithManager implements the complete controller registration for both embedded and external modes.
func (c *Controller) RegisterWithManager(mgr ctrl.Manager) error {
	// Register the CRD types with the scheme
	if err := v1alpha1.AddToScheme(mgr.GetScheme()); err != nil {
		return err
	}
	if err := c.setupReconciler(mgr); err != nil {
		return err
	}
	return c.setupWebhookWithRuntimeConfig(mgr)
}

// NotaryValidator validates Notary resources via webhook (for external controllers).
type NotaryValidator struct{}

// ValidateCreate implements ctrlwebhookadmission.CustomValidator.
func (v *NotaryValidator) ValidateCreate(ctx context.Context, obj runtime.Object) (ctrlwebhookadmission.Warnings, error) {
	notary, ok := obj.(*v1alpha1.Notary)
	if !ok {
		return nil, fmt.Errorf("expected a Notary object but got %T", obj)
	}
	return v.validateNotary(ctx, notary)
}

// ValidateUpdate implements ctrlwebhookadmission.CustomValidator.
func (v *NotaryValidator) ValidateUpdate(ctx context.Context, _, newObj runtime.Object) (ctrlwebhookadmission.Warnings, error) {
	notary, ok := newObj.(*v1alpha1.Notary)
	if !ok {
		return nil, fmt.Errorf("expected a Notary object but got %T", newObj)
	}
	return v.validateNotary(ctx, notary)
}

// ValidateDelete implements ctrlwebhookadmission.CustomValidator.
func (v *NotaryValidator) ValidateDelete(context.Context, runtime.Object) (ctrlwebhookadmission.Warnings, error) {
	// Allow all deletions
	return nil, nil
}

// validateNotary performs the actual validation logic.
func (v *NotaryValidator) validateNotary(ctx context.Context, notary *v1alpha1.Notary) (ctrlwebhookadmission.Warnings, error) {
	// Check if this is a dry run request
	if req, err := ctrlwebhookadmission.RequestFromContext(ctx); err == nil {
		if req.DryRun != nil && *req.DryRun {
			klog.V(1).Infof("[DryRun] Webhook validating Notary %s/%s\n", req.Namespace, req.Name)
		}
	}
	return ValidateNotary(notary)
}
