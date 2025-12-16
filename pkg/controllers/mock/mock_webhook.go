// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package mock

import (
	"context"
	"errors"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlwebhookadmission "sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/base"
)

func (c *controller) setupWebhookWithManager(mgr ctrl.Manager) error {
	mgr.GetLogger().Info("Setting up Mock Namespace webhook")

	// set up the container controller with a webhook which prevents all modification.
	mutatingConfig := base.WebhookConfig{
		Name:        "mock-namespace-no-delete",
		WebhookName: "mock-namespace-no-delete.mock.rancherdesktop.io",
		WebhookPort: c.webhookPort,
		Operations: []admissionregistrationv1.OperationType{
			admissionregistrationv1.Delete,
		},
		Validator: &staticNamespaceNoDeleteValidator{},
	}

	managers, err := base.SetupWebhookForResource(mgr, &corev1.Namespace{}, mutatingConfig)
	if err != nil {
		mgr.GetLogger().Error(err, "Failed to set up Mock Namespace webhook")
		return err
	}
	c.webhookManagers = append(c.webhookManagers, managers...)

	return nil
}

func (c *controller) GetWebhookServiceName() string {
	return controllerName + "-webhook"
}

func (c *controller) GetWebhookManagers() []*base.WebhookManager {
	return c.webhookManagers
}

type staticNamespaceNoDeleteValidator struct{}

// ValidateCreate implements ctrlwebhookadmission.CustomValidator.
func (c *staticNamespaceNoDeleteValidator) ValidateCreate(_ context.Context, _ runtime.Object) (warnings ctrlwebhookadmission.Warnings, err error) {
	return nil, nil
}

// ValidateDelete implements ctrlwebhookadmission.CustomValidator.
func (c *staticNamespaceNoDeleteValidator) ValidateDelete(ctx context.Context, obj runtime.Object) (warnings ctrlwebhookadmission.Warnings, err error) {
	ns, ok := obj.(*corev1.Namespace)
	if !ok {
		return nil, errors.New("object is not a Namespace")
	}
	if ns.Name == mockNamespaceName {
		klog.FromContext(ctx).V(4).Info("Rejecting delete of namespace")
		return nil, apierrors.NewConflict(
			schema.GroupResource{Group: "", Resource: "Namespace"},
			ns.Name,
			errors.New("deletion of mock namespace is not allowed"),
		)
	}
	return nil, nil
}

// ValidateUpdate implements ctrlwebhookadmission.CustomValidator.
func (c *staticNamespaceNoDeleteValidator) ValidateUpdate(_ context.Context, _, _ runtime.Object) (warnings ctrlwebhookadmission.Warnings, err error) {
	return nil, nil
}
