// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/app/v1alpha1"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/base"
)

const (
	appName = "app"

	ConditionCreated = "Created"
	ConditionRunning = "Running"
)

type AppReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder events.EventRecorder
}

// Reconcile implements a singleton app reconciliation loop.
// The app controller is a cluster-scoped singleton - only one instance named 'app' is allowed.
func (r *AppReconciler) Reconcile(ctx context.Context, _ ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var app v1alpha1.App
	if err := r.Get(ctx, client.ObjectKey{Name: appName}, &app); err != nil {
		if client.IgnoreNotFound(err) != nil {
			log.Error(err, "Unable to fetch App singleton")
		}
		return ctrl.Result{}, err
	}

	if base.IsBeingDeleted(&app) {
		log.Info("App resource is being deleted, performing cleanup")
		return ctrl.Result{}, nil
	}

	if apimeta.IsStatusConditionTrue(app.Status.Conditions, ConditionRunning) {
		log.Info("App is running")
	} else {
		r.setCondition(&app, ConditionRunning, metav1.ConditionFalse, "NotRunning", "The app is not running")
		if err := r.Status().Update(ctx, &app); err != nil {
			log.Error(err, "Unable to update App status")
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

// setCondition updates or adds a condition in the App status.
func (r *AppReconciler) setCondition(app *v1alpha1.App, conditionType string, status metav1.ConditionStatus, reason, message string) {
	changed := apimeta.SetStatusCondition(&app.Status.Conditions, metav1.Condition{
		Type:    conditionType,
		Status:  status,
		Reason:  reason,
		Message: message,
	})
	if changed && r.Recorder != nil {
		r.Recorder.Eventf(app, nil, corev1.EventTypeNormal, "ConditionChanged", conditionType, message)
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *AppReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.App{}).
		Complete(r)
}
