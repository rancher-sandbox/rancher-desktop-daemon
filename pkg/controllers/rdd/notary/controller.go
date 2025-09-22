// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package notary

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlwebhookadmission "sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/rdd/v1alpha1"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/base"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/rdd/notary/controllers"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/instance"
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
	// WebhookName is the name used for the webhook configuration.
	WebhookName = "notary.rdd.rancherdesktop.io"
	// ValidatorConfigName is the name of the ValidatingWebhookConfiguration.
	ValidatorConfigName = "notary-validator"
)

//go:embed crd.yaml
var notaryCRD string

// Controller implements the base.Controller interface for notary.
type Controller struct {
	webhookPort int // The actual webhook port allocated by SharedControllerManager
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
	// 1. Register webhook validation with controller-runtime
	// Certificates are already handled by SharedControllerManager
	if err := ctrl.NewWebhookManagedBy(mgr).
		For(&v1alpha1.Notary{}).
		WithValidator(&NotaryValidator{}).
		Complete(); err != nil {
		return err
	}

	// 2. Create webhook configuration with proper CA bundle (async)
	klog.Info("Launching async webhook configuration creation goroutine...")
	go c.createWebhookConfigurationAsync(mgr)

	return nil
}

// createWebhookConfigurationAsync creates the webhook configuration with retry logic.
func (c *Controller) createWebhookConfigurationAsync(mgr ctrl.Manager) {
	klog.Info("Starting async webhook configuration creation...")

	// Wait for webhook server to be ready and discovery system to be populated
	klog.Info("Waiting 5 seconds for webhook server to be ready...")
	time.Sleep(5 * time.Second)

	klog.Info("Beginning webhook configuration creation attempts...")
	for i := range 10 {
		klog.Infof("Webhook configuration creation attempt %d/10", i+1)
		if err := c.createWebhookConfiguration(mgr); err != nil {
			klog.Errorf("Failed to create webhook configuration (attempt %d/10): %v", i+1, err)
			klog.Infof("Waiting 3 seconds before retry...")
			time.Sleep(3 * time.Second)
			continue
		}
		klog.Info("Successfully created webhook configuration")
		return
	}
	klog.Error("CRITICAL: Failed to create webhook configuration after 10 attempts")
}

// createWebhookConfiguration creates the ValidatingWebhookConfiguration with proper CA bundle.
func (c *Controller) createWebhookConfiguration(mgr ctrl.Manager) error {
	klog.Info("Creating webhook configuration...")
	ctx := context.Background()

	// Get CA bundle from instance TLS directory where certificates are persistently stored
	webhookCertDir := instance.TLSDir()
	klog.Infof("Using instance TLS certificate directory: %s", webhookCertDir)

	// Read CA bundle directly without creating a new certificate manager
	// The certificates were already created by SharedControllerManager
	caBundlePath := filepath.Join(webhookCertDir, base.DefaultWebhookCACertFileName)
	klog.Infof("Reading CA bundle from: %s", caBundlePath)
	caBundleBytes, err := os.ReadFile(caBundlePath)
	if err != nil {
		klog.Errorf("Failed to read CA bundle file: %v", err)
		return fmt.Errorf("failed to read CA bundle file %s: %w", caBundlePath, err)
	}
	klog.Infof("Got CA bundle of %d bytes", len(caBundleBytes))

	// Use the webhook port provided by SharedControllerManager
	serverIP := "127.0.0.1"
	webhookPort := c.webhookPort

	klog.Infof("Using webhook port from SharedControllerManager: %d", webhookPort)

	webhookURL := fmt.Sprintf("https://%s:%d/validate-rdd-rancherdesktop-io-v1alpha1-notary", serverIP, webhookPort)
	klog.Infof("Webhook URL: %s", webhookURL)

	failurePolicy := admissionregistrationv1.Fail
	sideEffects := admissionregistrationv1.SideEffectClassNone

	webhook := &admissionregistrationv1.ValidatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Name: ValidatorConfigName,
		},
		Webhooks: []admissionregistrationv1.ValidatingWebhook{
			{
				Name: WebhookName,
				ClientConfig: admissionregistrationv1.WebhookClientConfig{
					URL:      &webhookURL,
					CABundle: caBundleBytes,
				},
				Rules: []admissionregistrationv1.RuleWithOperations{
					{
						Operations: []admissionregistrationv1.OperationType{
							admissionregistrationv1.Create,
							admissionregistrationv1.Update,
						},
						Rule: admissionregistrationv1.Rule{
							APIGroups:   []string{v1alpha1.GroupVersion.Group},
							APIVersions: []string{v1alpha1.GroupVersion.Version},
							Resources:   []string{"notaries"},
						},
					},
				},
				FailurePolicy:           &failurePolicy,
				SideEffects:             &sideEffects,
				AdmissionReviewVersions: []string{"v1", "v1beta1"},
			},
		},
	}

	// Try to create or update the webhook configuration
	klog.Info("Getting Kubernetes client...")
	client := mgr.GetClient()
	if client == nil {
		return errors.New("manager client is nil")
	}

	klog.Info("Checking if webhook configuration already exists...")
	existingWebhook := &admissionregistrationv1.ValidatingWebhookConfiguration{}
	err = client.Get(ctx, types.NamespacedName{Name: ValidatorConfigName}, existingWebhook)
	if err != nil {
		klog.Infof("Webhook configuration does not exist (error: %v), creating new one...", err)
		// Create new webhook configuration
		if err := client.Create(ctx, webhook); err != nil {
			klog.Errorf("Failed to create webhook configuration: %v", err)
			return fmt.Errorf("failed to create webhook configuration: %w", err)
		}
		klog.Infof("Successfully created webhook configuration for %s", webhookURL)
	} else {
		klog.Info("Webhook configuration already exists, updating...")
		// Update existing webhook configuration
		existingWebhook.Webhooks = webhook.Webhooks
		if err := client.Update(ctx, existingWebhook); err != nil {
			klog.Errorf("Failed to update webhook configuration: %v", err)
			return fmt.Errorf("failed to update webhook configuration: %w", err)
		}
		klog.Infof("Successfully updated webhook configuration for %s", webhookURL)
	}

	klog.Info("Webhook configuration operation completed successfully")
	return nil
}

// RegisterWithManager implements the complete controller registration for both embedded and external modes.
func (c *Controller) RegisterWithManager(mgr ctrl.Manager) error {
	// Register the CRD types with the scheme
	if err := v1alpha1.AddToScheme(mgr.GetScheme()); err != nil {
		return err
	}

	// Set up the reconciler
	if err := c.setupReconciler(mgr); err != nil {
		return err
	}

	// Set up webhook with runtime configuration
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
func (v *NotaryValidator) ValidateUpdate(ctx context.Context, _oldObj, newObj runtime.Object) (ctrlwebhookadmission.Warnings, error) {
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
			// For dry run requests, we still perform validation but can skip side effects
			// In this case, we don't have side effects, so we proceed with normal validation
			// but log that this is a dry run
			fmt.Fprintf(os.Stdout, "[DryRun] Webhook validating Notary %s/%s\n", req.Namespace, req.Name)
		}
	}

	// Use shared validation logic with warnings support
	warnings, err := ValidateNotary(notary)
	if err != nil {
		// Note: We cannot generate events from the webhook validator since it doesn't have
		// access to the event recorder. Admission failures are logged by the API server
		// and visible through kubectl events, but will show as admission control errors.
		return nil, err
	}

	// Convert string warnings to ctrlwebhookadmission.Warnings
	var webhookWarnings ctrlwebhookadmission.Warnings
	for _, warning := range warnings {
		webhookWarnings = append(webhookWarnings, warning)
	}

	return webhookWarnings, nil
}
