// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gotest.tools/v3/assert"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	appv1alpha1 "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/app/v1alpha1"
)

// fakeK3sServer starts an httptest TLS server that answers /healthz with
// healthzStatus and writes a matching k3s-shaped kubeconfig to srcPath.
// Returns the configured kubeconfig path and registers t.Cleanup for the
// server.
func fakeK3sServer(t *testing.T, srcPath string, healthzStatus int) string {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/healthz" {
			w.WriteHeader(healthzStatus)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)

	certPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: srv.Certificate().Raw,
	})

	cfg := clientcmdapi.NewConfig()
	cfg.Clusters["default"] = &clientcmdapi.Cluster{
		Server:                   srv.URL,
		CertificateAuthorityData: certPEM,
	}
	cfg.AuthInfos["default"] = &clientcmdapi.AuthInfo{
		ClientCertificateData: certPEM,
		ClientKeyData:         clientKeyPEM(t, srv),
	}
	cfg.Contexts["default"] = &clientcmdapi.Context{
		Cluster:  "default",
		AuthInfo: "default",
	}
	cfg.CurrentContext = "default"

	assert.NilError(t, clientcmd.WriteToFile(*cfg, srcPath))
	return srcPath
}

// clientKeyPEM extracts the test server's private key in PEM form.
// httptest's self-signed cert ships with its own key, which probeK3sAPI
// requires when the kubeconfig declares ClientCertificateData / ClientKeyData.
func clientKeyPEM(t *testing.T, srv *httptest.Server) []byte {
	t.Helper()
	leaf := srv.TLS.Certificates[0]
	keyBytes, err := x509.MarshalPKCS8PrivateKey(leaf.PrivateKey)
	assert.NilError(t, err)
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyBytes})
}

// newAppRunning builds an App with Kubernetes enabled and the Running condition
// set to True so Reconcile reaches the probe path.
func newAppRunning() *appv1alpha1.App {
	return &appv1alpha1.App{
		ObjectMeta: metav1.ObjectMeta{Name: appName, Generation: 1},
		Spec: appv1alpha1.AppSpec{
			Running:    true,
			Kubernetes: appv1alpha1.KubernetesSpec{Enabled: true, Version: "1.32.0"},
		},
		Status: appv1alpha1.AppStatus{
			Conditions: []metav1.Condition{{
				Type:   appv1alpha1.AppConditionRunning,
				Status: metav1.ConditionTrue,
				Reason: "Started",
			}},
		},
	}
}

// newAppKubernetesDisabled builds an App with Kubernetes disabled and the
// Running condition True, so Reconcile takes the NotApplicable branch.
func newAppKubernetesDisabled() *appv1alpha1.App {
	app := newAppRunning()
	app.Spec.Kubernetes.Enabled = false
	return app
}

// newAppNotRunning builds an App with Kubernetes enabled but the Running
// condition False, so Reconcile takes the NotRunning branch.
func newAppNotRunning() *appv1alpha1.App {
	app := newAppRunning()
	app.Status.Conditions[0].Status = metav1.ConditionFalse
	app.Status.Conditions[0].Reason = "Stopped"
	return app
}

// isolateKubeconfig points HOME and KUBECONFIG at dir so the reconciler's
// kubeconfig writes cannot touch the developer's real ~/.kube/config even
// if a future refactor changes KUBECONFIG-precedence handling.
func isolateKubeconfig(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("HOME", dir)
	t.Setenv("KUBECONFIG", filepath.Join(dir, ".kube", "config"))
}

func newKubeScheme(t *testing.T) *k8sruntime.Scheme {
	t.Helper()
	s := k8sruntime.NewScheme()
	assert.NilError(t, appv1alpha1.AddToScheme(s))
	return s
}

// findKubeReady returns the KubernetesReady condition on app, or nil if absent.
func findKubeReady(app *appv1alpha1.App) *metav1.Condition {
	return apimeta.FindStatusCondition(app.Status.Conditions, appv1alpha1.AppConditionKubernetesReady)
}

func Test_Reconcile_KubernetesReady_Ready(t *testing.T) {
	dir := t.TempDir()
	srcPath := fakeK3sServer(t, filepath.Join(dir, "k3s.yaml"), http.StatusOK)
	isolateKubeconfig(t, dir)

	scheme := newKubeScheme(t)
	app := newAppRunning()
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(app).
		WithStatusSubresource(app).
		Build()

	r := &KubernetesReconciler{
		Client:        c,
		K3sConfigPath: srcPath,
	}
	// removeKubeContext cancels the in-flight current-context probe and waits
	// for the goroutine started by manageKubeContext to finish, so it cannot
	// still be reading tempdir state after the test returns.
	t.Cleanup(func() { r.removeKubeContext(context.Background()) })

	req := ctrl.Request{NamespacedName: client.ObjectKey{Name: appName}}
	result, err := r.Reconcile(context.Background(), req)
	assert.NilError(t, err)
	assert.Equal(t, result.RequeueAfter, time.Duration(0),
		"Ready path should not request a requeue")

	got := &appv1alpha1.App{}
	assert.NilError(t, c.Get(context.Background(), client.ObjectKey{Name: appName}, got))

	cond := findKubeReady(got)
	assert.Assert(t, cond != nil, "KubernetesReady condition missing")
	assert.Equal(t, cond.Status, metav1.ConditionTrue)
	assert.Equal(t, cond.Reason, appv1alpha1.AppKubernetesReasonReady)
}

func Test_Reconcile_KubernetesReady_MergeFailed(t *testing.T) {
	dir := t.TempDir()
	srcPath := fakeK3sServer(t, filepath.Join(dir, "k3s.yaml"), http.StatusOK)

	// Plant a regular file where the destination kubeconfig's parent
	// directory should be. createReplaceKubeContext calls
	// os.MkdirAll("$HOME/.kube"), which fails with ENOTDIR.
	kubeDir := filepath.Join(dir, ".kube")
	assert.NilError(t, os.WriteFile(kubeDir, []byte("not a directory"), 0o600))
	isolateKubeconfig(t, dir)

	scheme := newKubeScheme(t)
	app := newAppRunning()
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(app).
		WithStatusSubresource(app).
		Build()

	r := &KubernetesReconciler{
		Client:        c,
		K3sConfigPath: srcPath,
	}

	req := ctrl.Request{NamespacedName: client.ObjectKey{Name: appName}}
	result, err := r.Reconcile(context.Background(), req)
	assert.NilError(t, err)
	assert.Equal(t, result.RequeueAfter, kubeProbeRequeue,
		"MergeFailed path should requeue after kubeProbeRequeue")

	got := &appv1alpha1.App{}
	assert.NilError(t, c.Get(context.Background(), client.ObjectKey{Name: appName}, got))

	cond := findKubeReady(got)
	assert.Assert(t, cond != nil, "KubernetesReady condition missing")
	assert.Equal(t, cond.Status, metav1.ConditionFalse)
	assert.Equal(t, cond.Reason, appv1alpha1.AppKubernetesReasonMergeFailed)
	assert.Assert(t, cond.Message != "",
		"MergeFailed condition should carry the underlying error in its message")
}

func Test_Reconcile_KubernetesReady_NotApplicable(t *testing.T) {
	dir := t.TempDir()
	isolateKubeconfig(t, dir)

	scheme := newKubeScheme(t)
	app := newAppKubernetesDisabled()
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(app).
		WithStatusSubresource(app).
		Build()

	r := &KubernetesReconciler{Client: c}

	req := ctrl.Request{NamespacedName: client.ObjectKey{Name: appName}}
	result, err := r.Reconcile(context.Background(), req)
	assert.NilError(t, err)
	assert.Equal(t, result.RequeueAfter, time.Duration(0),
		"NotApplicable path should not request a requeue")

	got := &appv1alpha1.App{}
	assert.NilError(t, c.Get(context.Background(), client.ObjectKey{Name: appName}, got))

	cond := findKubeReady(got)
	assert.Assert(t, cond != nil, "KubernetesReady condition missing")
	assert.Equal(t, cond.Status, metav1.ConditionFalse)
	assert.Equal(t, cond.Reason, appv1alpha1.AppKubernetesReasonNotApplicable)
}

func Test_Reconcile_KubernetesReady_NotRunning(t *testing.T) {
	dir := t.TempDir()
	isolateKubeconfig(t, dir)

	scheme := newKubeScheme(t)
	app := newAppNotRunning()
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(app).
		WithStatusSubresource(app).
		Build()

	r := &KubernetesReconciler{Client: c}

	req := ctrl.Request{NamespacedName: client.ObjectKey{Name: appName}}
	result, err := r.Reconcile(context.Background(), req)
	assert.NilError(t, err)
	assert.Equal(t, result.RequeueAfter, time.Duration(0),
		"NotRunning path should not request a requeue")

	got := &appv1alpha1.App{}
	assert.NilError(t, c.Get(context.Background(), client.ObjectKey{Name: appName}, got))

	cond := findKubeReady(got)
	assert.Assert(t, cond != nil, "KubernetesReady condition missing")
	assert.Equal(t, cond.Status, metav1.ConditionFalse)
	assert.Equal(t, cond.Reason, appv1alpha1.AppKubernetesReasonNotRunning)
}

func Test_Reconcile_KubernetesReady_Probing_ProbeError(t *testing.T) {
	dir := t.TempDir()
	isolateKubeconfig(t, dir)

	scheme := newKubeScheme(t)
	app := newAppRunning()
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(app).
		WithStatusSubresource(app).
		Build()

	// Point K3sConfigPath at a file that does not exist so probeK3sAPI
	// returns the (false, err) "kubeconfig unreadable" path.
	r := &KubernetesReconciler{
		Client:        c,
		K3sConfigPath: filepath.Join(dir, "missing-k3s.yaml"),
	}

	req := ctrl.Request{NamespacedName: client.ObjectKey{Name: appName}}
	result, err := r.Reconcile(context.Background(), req)
	assert.NilError(t, err)
	assert.Equal(t, result.RequeueAfter, kubeProbeRequeue,
		"Probing path should requeue after kubeProbeRequeue")

	got := &appv1alpha1.App{}
	assert.NilError(t, c.Get(context.Background(), client.ObjectKey{Name: appName}, got))

	cond := findKubeReady(got)
	assert.Assert(t, cond != nil, "KubernetesReady condition missing")
	assert.Equal(t, cond.Status, metav1.ConditionFalse)
	assert.Equal(t, cond.Reason, appv1alpha1.AppKubernetesReasonProbing)
}

func Test_Reconcile_KubernetesReady_Probing_Unhealthy(t *testing.T) {
	dir := t.TempDir()
	// fakeK3sServer answers /healthz with 500, so probeK3sAPI takes the
	// (false, nil) "server reachable but unhealthy" path.
	srcPath := fakeK3sServer(t, filepath.Join(dir, "k3s.yaml"), http.StatusInternalServerError)
	isolateKubeconfig(t, dir)

	scheme := newKubeScheme(t)
	app := newAppRunning()
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(app).
		WithStatusSubresource(app).
		Build()

	r := &KubernetesReconciler{
		Client:        c,
		K3sConfigPath: srcPath,
	}

	req := ctrl.Request{NamespacedName: client.ObjectKey{Name: appName}}
	result, err := r.Reconcile(context.Background(), req)
	assert.NilError(t, err)
	assert.Equal(t, result.RequeueAfter, kubeProbeRequeue,
		"Probing path should requeue after kubeProbeRequeue")

	got := &appv1alpha1.App{}
	assert.NilError(t, c.Get(context.Background(), client.ObjectKey{Name: appName}, got))

	cond := findKubeReady(got)
	assert.Assert(t, cond != nil, "KubernetesReady condition missing")
	assert.Equal(t, cond.Status, metav1.ConditionFalse)
	assert.Equal(t, cond.Reason, appv1alpha1.AppKubernetesReasonProbing)
}
