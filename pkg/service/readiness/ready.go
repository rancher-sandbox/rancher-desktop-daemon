// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors
// SPDX-FileCopyrightText: The KCP Authors

package readiness

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsclientset "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"
	"sigs.k8s.io/yaml"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/base"
)

// WaitForReady waits for the control plane to be ready.
func WaitForReady(ctx context.Context, config *rest.Config, logging bool) error {
	logger := klog.FromContext(ctx)
	if logging {
		logger.Info("Waiting for /readyz to succeed")
	}
	lastSeenUnready := sets.New[string]()

	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		return err
	}

	for {
		time.Sleep(100 * time.Millisecond)

		select {
		case <-ctx.Done():
			return errors.New("context canceled")
		default:
		}

		res := client.RESTClient().Get().AbsPath("/readyz").Do(ctx)
		if _, err := res.Raw(); err != nil {
			unreadyComponents := unreadyComponentsFromError(err)
			if !lastSeenUnready.Equal(unreadyComponents) {
				klog.ErrorS(err, "Control plane not ready", "unreadyComponents", sets.List[string](unreadyComponents))
				lastSeenUnready = unreadyComponents
			}
		}

		// When there is an error for invalid certificate, we should exit immediately as there is no point in retrying.
		if res.Error() != nil {
			if isCertificateError(res.Error()) {
				logger.Error(res.Error(), "control plane not ready")
				logger.Info("This is likely due to certificates folder containing invalid certificates. Please fix them and restart the control plane.")
				return res.Error()
			}
		}

		var rc int
		res.StatusCode(&rc)
		if rc == http.StatusOK {
			if logging {
				logger.Info("Control plane is ready")
			}
			break
		}

		// Always log non-ready status for debugging
		klog.V(1).InfoS("Control plane not ready", "status", rc, "unreadyComponents", sets.List[string](lastSeenUnready))
	}

	return nil
}

// WaitForReadyWithCRDs waits for the control plane to be ready and all expected CRDs to be established.
func WaitForReadyWithCRDs(ctx context.Context, config *rest.Config, expectedControllers []base.Controller, logging bool) error {
	logger := klog.FromContext(ctx)

	// First wait for basic readiness
	if logging {
		logger.Info("Waiting for basic control plane readiness")
	}
	if err := WaitForReady(ctx, config, logging); err != nil {
		return err
	}

	if logging {
		logger.Info("Control plane ready, now checking CRDs", "controllers", len(expectedControllers))
	}

	// Then wait for CRDs to be established
	if err := waitForCRDsEstablished(ctx, config, expectedControllers, logging); err != nil {
		logger.Error(err, "CRD establishment failed")
		return err
	}

	if logging {
		logger.Info("CRDs established, now checking webhook configurations")
	}

	// Finally wait for webhook configurations to be created
	if err := waitForWebhookConfigurations(ctx, config, expectedControllers, logging); err != nil {
		logger.Error(err, "Webhook configuration check failed")
		return err
	}

	if logging {
		logger.Info("All readiness checks completed successfully")
	}
	return nil
}

// waitForCRDsEstablished waits for all CRDs from the given controllers to be established.
func waitForCRDsEstablished(ctx context.Context, config *rest.Config, controllers []base.Controller, logging bool) error {
	if len(controllers) == 0 {
		if logging {
			klog.FromContext(ctx).Info("No controllers specified, skipping CRD readiness check")
		}
		return nil
	}

	logger := klog.FromContext(ctx)
	if logging {
		logger.Info("Waiting for CRDs to be established", "controllers", len(controllers))
	}

	apiextensionsClient, err := apiextensionsclientset.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("failed to create apiextensions client: %w", err)
	}

	crdClient := apiextensionsClient.ApiextensionsV1().CustomResourceDefinitions()

	// Extract CRD names from controllers
	var expectedCRDNames []string
	for _, controller := range controllers {
		crdData := controller.GetCRDData()
		if crdData == "" {
			continue // Skip controllers without CRDs
		}

		var crd apiextensionsv1.CustomResourceDefinition
		if err := yaml.Unmarshal([]byte(crdData), &crd); err != nil {
			return fmt.Errorf("failed to unmarshal CRD for controller %s: %w", controller.GetName(), err)
		}
		expectedCRDNames = append(expectedCRDNames, crd.Name)
	}

	if len(expectedCRDNames) == 0 {
		if logging {
			logger.Info("No CRDs found in controllers, skipping CRD readiness check")
		}
		return nil
	}

	if logging {
		logger.Info("Checking CRD establishment", "expectedCRDs", expectedCRDNames)
	}

	expectedCRDs := sets.New(expectedCRDNames...)
	establishedCRDs := sets.New[string]()
	var mu sync.Mutex

	// First, check if any CRDs are already established
	for _, crdName := range expectedCRDNames {
		crd, err := crdClient.Get(ctx, crdName, metav1.GetOptions{})
		if err != nil {
			if logging {
				logger.V(2).Info("CRD not yet available, will watch for it", "crd", crdName)
			}
			continue // Will catch it in the watch
		}

		if isCRDEstablished(crd) {
			mu.Lock()
			establishedCRDs.Insert(crdName)
			mu.Unlock()
			if logging {
				logger.V(1).Info("CRD already established", "crd", crdName)
			}
		}
	}

	// Check if all are already established
	mu.Lock()
	allEstablished := establishedCRDs.Equal(expectedCRDs)
	mu.Unlock()

	if allEstablished {
		if logging {
			logger.Info("All CRDs are already established")
		}
		return nil
	}

	// Watch for CRD changes
	watchCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	watcher, err := crdClient.Watch(watchCtx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to start CRD watch: %w", err)
	}
	defer watcher.Stop()

	klog.InfoS("Starting CRD watch", "timeout", "60s", "expected", expectedCRDNames)

	// After starting the watch, do one more check to catch any CRDs that
	// became established in the brief window between the initial check and watch start
	for _, crdName := range expectedCRDNames {
		if establishedCRDs.Has(crdName) {
			continue // Already marked as established
		}

		crd, err := crdClient.Get(ctx, crdName, metav1.GetOptions{})
		if err != nil {
			continue // Will catch it in the watch
		}

		if isCRDEstablished(crd) {
			mu.Lock()
			wasNew := !establishedCRDs.Has(crdName)
			establishedCRDs.Insert(crdName)
			allEstablished = establishedCRDs.Equal(expectedCRDs)
			mu.Unlock()

			if logging && wasNew {
				logger.Info("CRD became established during watch setup", "crd", crdName)
			}

			if allEstablished {
				if logging {
					logger.Info("All CRDs are established after watch setup")
				}
				return nil
			}
		}
	}

	for {
		select {
		case <-watchCtx.Done():
			mu.Lock()
			established := sets.List(establishedCRDs)
			missing := expectedCRDs.Difference(establishedCRDs)
			mu.Unlock()
			klog.ErrorS(watchCtx.Err(), "CRD establishment timeout", "established", established, "missing", sets.List(missing), "expected", expectedCRDNames)
			return errors.New("timeout waiting for CRDs to be established")
		case event, ok := <-watcher.ResultChan():
			if !ok {
				return errors.New("watch channel closed unexpectedly")
			}

			crd, ok := event.Object.(*apiextensionsv1.CustomResourceDefinition)
			if !ok {
				continue
			}

			// Only care about CRDs we're waiting for
			if !expectedCRDs.Has(crd.Name) {
				continue
			}

			if event.Type == watch.Added || event.Type == watch.Modified {
				if isCRDEstablished(crd) {
					mu.Lock()
					wasNew := !establishedCRDs.Has(crd.Name)
					establishedCRDs.Insert(crd.Name)
					allEstablished := establishedCRDs.Equal(expectedCRDs)
					mu.Unlock()

					if logging && wasNew {
						logger.Info("CRD became established", "crd", crd.Name)
					}

					if allEstablished {
						if logging {
							logger.Info("All CRDs are established")
						}
						return nil
					}
				}
			}
		}
	}
}

// isCRDEstablished checks if a CRD has the Established condition set to true.
func isCRDEstablished(crd *apiextensionsv1.CustomResourceDefinition) bool {
	for _, condition := range crd.Status.Conditions {
		if condition.Type == apiextensionsv1.Established && condition.Status == apiextensionsv1.ConditionTrue {
			return true
		}
	}
	return false
}

// waitForWebhookConfigurations waits for all webhook configurations from the given controllers to be created.
func waitForWebhookConfigurations(ctx context.Context, config *rest.Config, controllers []base.Controller, logging bool) error {
	if len(controllers) == 0 {
		if logging {
			klog.FromContext(ctx).Info("No controllers specified, skipping webhook configuration readiness check")
		}
		return nil
	}

	logger := klog.FromContext(ctx)
	if logging {
		logger.Info("Waiting for webhook configurations", "controllers", len(controllers))
	}

	// Check which controllers need webhook configurations
	var expectedWebhookNames []string
	for _, controller := range controllers {
		// Check if controller implements WebhookController interface
		if _, ok := controller.(base.WebhookController); ok {
			controllerName := controller.GetName()
			webhookName := getWebhookConfigurationName(controllerName)
			expectedWebhookNames = append(expectedWebhookNames, webhookName)
			if logging {
				logger.Info("Controller has webhook configuration", "controller", controllerName, "webhook", webhookName)
			}
		}
	}

	if len(expectedWebhookNames) == 0 {
		if logging {
			logger.Info("No webhook configurations expected, skipping webhook readiness check")
		}
		return nil
	}

	if logging {
		logger.Info("Waiting for webhook configurations to be created", "webhooks", expectedWebhookNames)
	}

	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	webhookClient := client.AdmissionregistrationV1().ValidatingWebhookConfigurations()

	expectedWebhooks := sets.New(expectedWebhookNames...)
	foundWebhooks := sets.New[string]()
	var mu sync.Mutex

	// First, check if any webhooks are already created
	for _, webhookName := range expectedWebhookNames {
		_, err := webhookClient.Get(ctx, webhookName, metav1.GetOptions{})
		if err == nil {
			mu.Lock()
			foundWebhooks.Insert(webhookName)
			mu.Unlock()
			if logging {
				logger.V(1).Info("Webhook configuration already exists", "webhook", webhookName)
			}
		}
	}

	// Check if all are already created
	mu.Lock()
	allFound := foundWebhooks.Equal(expectedWebhooks)
	mu.Unlock()

	if allFound {
		if logging {
			logger.Info("All webhook configurations already exist")
		}
		return nil
	}

	// Wait for webhook configurations to be created (they are created asynchronously)
	// Use a reasonable timeout since webhook creation can take several seconds
	waitCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	if logging {
		logger.Info("Polling for webhook configuration creation")
	}

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-waitCtx.Done():
			mu.Lock()
			missing := expectedWebhooks.Difference(foundWebhooks)
			mu.Unlock()
			return fmt.Errorf("timeout waiting for webhook configurations to be created: missing %v", sets.List(missing))
		case <-ticker.C:
			// Check for any missing webhook configurations
			for _, webhookName := range expectedWebhookNames {
				mu.Lock()
				alreadyFound := foundWebhooks.Has(webhookName)
				mu.Unlock()

				if alreadyFound {
					continue
				}

				_, err := webhookClient.Get(ctx, webhookName, metav1.GetOptions{})
				if err == nil {
					mu.Lock()
					foundWebhooks.Insert(webhookName)
					allFound := foundWebhooks.Equal(expectedWebhooks)
					mu.Unlock()

					if logging {
						logger.Info("Webhook configuration became available", "webhook", webhookName)
					}

					if allFound {
						if logging {
							logger.Info("All webhook configurations are ready")
						}
						return nil
					}
				}
			}
		}
	}
}

// getWebhookConfigurationName returns the expected webhook configuration name for a controller.
func getWebhookConfigurationName(controllerName string) string {
	// Based on the notary controller pattern: "notary-validator"
	return controllerName + "-validator"
}

// there doesn't seem to be any simple way to get a metav1.Status from the Go client, so we get
// the content in a string-formatted error, unfortunately.
func unreadyComponentsFromError(err error) sets.Set[string] {
	innerErr := strings.TrimPrefix(strings.TrimSuffix(err.Error(), `") has prevented the request from succeeding`), `an error on the server ("`)
	unreadyComponents := sets.New[string]()
	for _, line := range strings.Split(innerErr, `\n`) {
		if name := strings.TrimPrefix(strings.TrimSuffix(line, ` failed: reason withheld`), `[-]`); name != line {
			// NB: sometimes the error we get is truncated (server-side?) to something like:
			// `\n[-]poststar") has prevented the request from succeeding` # spellchecker:ignore
			// In those cases, the `name` here is also truncated, but nothing we can do about that.
			// For that reason, the list of components returned is not durable and should not be parsed.
			unreadyComponents.Insert(name)
		}
	}
	return unreadyComponents
}

// isCertificateError checks if an error is related to x509 certificate verification.
func isCertificateError(err error) bool {
	if err == nil {
		return false
	}

	// Check for x509 certificate verification errors
	var x509Err x509.CertificateInvalidError
	if errors.As(err, &x509Err) {
		return true
	}

	var unknownAuthorityErr x509.UnknownAuthorityError
	if errors.As(err, &unknownAuthorityErr) {
		return true
	}

	var hostNameErr x509.HostnameError
	if errors.As(err, &hostNameErr) {
		return true
	}

	var constraintErr x509.ConstraintViolationError
	if errors.As(err, &constraintErr) {
		return true
	}

	// Fallback: check if the error message contains x509 certificate verification strings
	// This handles cases where the error is wrapped and doesn't directly expose the x509 types
	errMsg := err.Error()
	return strings.Contains(errMsg, "failed to verify certificate") ||
		strings.Contains(errMsg, "x509:")
}
