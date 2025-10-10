// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package base

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/instance"
)

// GenerateValidatingWebhookPath generates the webhook path that controller-runtime uses
// when registering a validating webhook for a given API group, version, and kind.
// This follows controller-runtime's convention: /validate-{group}-{version}-{kind}
// For core resources (empty group), the path becomes: /validate--{version}-{kind}.
// Dots in the API group are replaced with dashes.
//
// Example: GenerateValidatingWebhookPath("lima.rancherdesktop.io", "v1alpha1", "limavm")
// Returns: "/validate-lima-rancherdesktop-io-v1alpha1-limavm".
func GenerateValidatingWebhookPath(apiGroup, apiVersion, kind string) string {
	group := strings.ReplaceAll(apiGroup, ".", "-")
	return fmt.Sprintf("/validate-%s-%s-%s", group, apiVersion, kind)
}

// WebhookConfig contains configuration for creating a ValidatingWebhookConfiguration.
type WebhookConfig struct {
	// Name is the name of the ValidatingWebhookConfiguration resource
	Name string

	// WebhookName is the name field within the webhook configuration
	WebhookName string

	// WebhookPath is the URL path for the webhook endpoint (e.g., "/validate-rdd-rancherdesktop-io-v1alpha1-notary")
	WebhookPath string

	// APIGroup is the API group for the webhook rules (e.g., "rdd.rancherdesktop.io")
	APIGroup string

	// APIVersion is the API version for the webhook rules (e.g., "v1alpha1")
	APIVersion string

	// Resource is the resource type for the webhook rules (e.g., "notaries")
	// TODO: Consider deriving this automatically from the CRD or RESTMapper to maintain
	// a single source of truth. Currently must match the plural name in the CRD.
	Resource string

	// WebhookPort is the port the webhook server is listening on
	WebhookPort int

	// FailurePolicy determines how the API server handles webhook failures
	// Default is Fail if not specified
	FailurePolicy *admissionregistrationv1.FailurePolicyType
}

// WebhookManager handles the creation and management of ValidatingWebhookConfigurations.
type WebhookManager struct {
	config WebhookConfig
	mgr    ctrl.Manager
}

// newWebhookManager creates a new WebhookManager with the given configuration.
func newWebhookManager(config WebhookConfig, mgr ctrl.Manager) *WebhookManager {
	return &WebhookManager{
		config: config,
		mgr:    mgr,
	}
}

// SetupWebhookForResource is a helper function that sets up a webhook for a resource type.
// It registers the webhook with controller-runtime and creates a WebhookManager for later setup.
// The validator parameter must implement the admission.CustomValidator interface.
// Returns the created WebhookManager for storage in the controller.
func SetupWebhookForResource(mgr ctrl.Manager, obj client.Object, validator admission.CustomValidator, config WebhookConfig) (*WebhookManager, error) {
	// Register webhook validation with controller-runtime
	builder := ctrl.NewWebhookManagedBy(mgr).For(obj).WithValidator(validator)

	if err := builder.Complete(); err != nil {
		return nil, err
	}

	// Create and return webhook manager for parallel setup
	return newWebhookManager(config, mgr), nil
}

// Setup creates the webhook configuration with retry logic.
// This blocks until the webhook is successfully registered or max attempts are exceeded.
// This should be called after the webhook server is registered with the manager.
func (wm *WebhookManager) Setup() error {
	klog.Info("Starting webhook configuration creation...")

	const maxAttempts = 20
	const retryDelay = 3 * time.Second

	for attempt := 1; ; attempt++ {
		klog.Infof("Webhook configuration creation attempt %d/%d", attempt, maxAttempts)
		err := wm.createWebhookConfiguration()
		if err == nil {
			klog.Info("Successfully created webhook configuration")
			return nil
		}
		klog.Errorf("Failed to create webhook configuration (attempt %d/%d): %v", attempt, maxAttempts, err)
		if attempt >= maxAttempts {
			return fmt.Errorf("failed to create webhook configuration after %d attempts", maxAttempts)
		}
		klog.Infof("Waiting %v before retry...", retryDelay)
		time.Sleep(retryDelay)
	}
}

// createWebhookConfiguration creates the ValidatingWebhookConfiguration.
func (wm *WebhookManager) createWebhookConfiguration() error {
	klog.Info("Creating webhook configuration...")
	ctx := context.Background()

	// Read CA bundle directly without creating a new certificate manager.
	// The certificates were already created by SharedControllerManager.
	caBundlePath := filepath.Join(instance.TLSDir(), DefaultWebhookCACertFileName)
	klog.Infof("Reading CA bundle from: %s", caBundlePath)
	caBundleBytes, err := os.ReadFile(caBundlePath)
	if err != nil {
		klog.Errorf("Failed to read CA bundle file: %v", err)
		return fmt.Errorf("failed to read CA bundle file %s: %w", caBundlePath, err)
	}

	webhookURL := fmt.Sprintf("https://127.0.0.1:%d%s", wm.config.WebhookPort, wm.config.WebhookPath)
	klog.Infof("Webhook URL: %s", webhookURL)

	// Set default failure policy if not specified
	failurePolicy := admissionregistrationv1.Fail
	if wm.config.FailurePolicy != nil {
		failurePolicy = *wm.config.FailurePolicy
	}
	sideEffects := admissionregistrationv1.SideEffectClassNone

	webhook := &admissionregistrationv1.ValidatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Name: wm.config.Name,
		},
		Webhooks: []admissionregistrationv1.ValidatingWebhook{
			{
				Name: wm.config.WebhookName,
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
							APIGroups:   []string{wm.config.APIGroup},
							APIVersions: []string{wm.config.APIVersion},
							Resources:   []string{wm.config.Resource},
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
	client := wm.mgr.GetClient()
	if client == nil {
		return errors.New("manager client is nil")
	}

	klog.Info("Checking if webhook configuration already exists...")
	existingWebhook := &admissionregistrationv1.ValidatingWebhookConfiguration{}
	err = client.Get(ctx, types.NamespacedName{Name: wm.config.Name}, existingWebhook)
	if err != nil {
		klog.Infof("Webhook configuration does not exist (error: %v), creating new one...", err)
		if err := client.Create(ctx, webhook); err != nil {
			klog.Errorf("Failed to create webhook configuration: %v", err)
			return fmt.Errorf("failed to create webhook configuration: %w", err)
		}
		klog.Infof("Successfully created webhook configuration for %s", webhookURL)
	} else {
		klog.Info("Webhook configuration already exists, updating...")
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
