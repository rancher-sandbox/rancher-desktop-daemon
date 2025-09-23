// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"context"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/lima/v1alpha1"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/base"
)

const (
	// FinalizerName is the finalizer name used for LimaVM cleanup.
	FinalizerName = "rdd.rancherdesktop.io/limavm-cleanup"
)

// LimaVMReconciler reconciles a LimaVM object.
type LimaVMReconciler struct {
	client.Client
	Scheme          *runtime.Scheme
	FinalizerHelper *base.FinalizerHelper
}

// +kubebuilder:rbac:groups=lima.rancherdesktop.io,resources=limavms,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=lima.rancherdesktop.io,resources=limavms/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=lima.rancherdesktop.io,resources=limavms/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the LimaVM object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.21.0/pkg/reconcile
func (r *LimaVMReconciler) Reconcile(ctx context.Context, _ ctrl.Request) (ctrl.Result, error) {
	_ = logf.FromContext(ctx)

	// TODO(user): your logic here

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *LimaVMReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.LimaVM{}).
		Named("limavm").
		Complete(r)
}
