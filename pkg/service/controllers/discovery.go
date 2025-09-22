// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/instance"
)

const (
	// ControllerManagerConfigMapName is the name of the ConfigMap that stores controller manager information.
	ControllerManagerConfigMapName = "rdd-controller-manager"

	// RDDSystemNamespace is the namespace where RDD stores its control plane information.
	RDDSystemNamespace = "rdd-system"
)

// ControllerManagerDiscovery handles service discovery for the shared controller manager.
type ControllerManagerDiscovery struct {
	client    kubernetes.Interface
	namespace string
	instance  string
}

// NewControllerManagerDiscovery creates a new discovery service.
func NewControllerManagerDiscovery(config *rest.Config) (*ControllerManagerDiscovery, error) {
	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	return &ControllerManagerDiscovery{
		client:    client,
		namespace: RDDSystemNamespace,
		instance:  instance.Name(),
	}, nil
}

// RegisterControllerManager creates or updates the ConfigMap with controller manager information.
func (d *ControllerManagerDiscovery) RegisterControllerManager(ctx context.Context, healthPort, metricsPort int, controllers []string) error {
	if err := d.ensureNamespace(ctx); err != nil {
		return fmt.Errorf("failed to ensure namespace: %w", err)
	}

	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ControllerManagerConfigMapName,
			Namespace: d.namespace,
			Labels: map[string]string{
				"app.kubernetes.io/name":      "rancher-desktop-daemon",
				"app.kubernetes.io/component": "controller-manager",
				"app.kubernetes.io/instance":  d.instance,
			},
		},
		Data: map[string]string{
			"instance":           d.instance,
			"healthPort":         strconv.Itoa(healthPort),
			"metricsPort":        strconv.Itoa(metricsPort),
			"enabledControllers": strings.Join(controllers, ","),
			"startTime":          time.Now().UTC().Format(time.RFC3339),
			"healthEndpoint":     fmt.Sprintf("http://localhost:%d/healthz", healthPort),
			"metricsEndpoint":    fmt.Sprintf("http://localhost:%d/metrics", metricsPort),
		},
	}

	// Try to update existing ConfigMap, create if it doesn't exist
	_, err := d.client.CoreV1().ConfigMaps(d.namespace).Update(ctx, configMap, metav1.UpdateOptions{})
	if errors.IsNotFound(err) {
		_, err = d.client.CoreV1().ConfigMaps(d.namespace).Create(ctx, configMap, metav1.CreateOptions{})
	}

	if err != nil {
		return fmt.Errorf("failed to register controller manager: %w", err)
	}

	klog.InfoS("Registered controller manager in cluster",
		"namespace", d.namespace,
		"configmap", ControllerManagerConfigMapName,
		"instance", d.instance,
		"controllers", len(controllers))

	return nil
}

// UnregisterControllerManager removes the controller manager ConfigMap.
func (d *ControllerManagerDiscovery) UnregisterControllerManager(ctx context.Context) error {
	err := d.client.CoreV1().ConfigMaps(d.namespace).Delete(ctx, ControllerManagerConfigMapName, metav1.DeleteOptions{})
	if errors.IsNotFound(err) {
		return nil // Already deleted, no error
	}
	if err != nil {
		return fmt.Errorf("failed to unregister controller manager: %w", err)
	}

	klog.InfoS("Unregistered controller manager from cluster",
		"namespace", d.namespace,
		"configmap", ControllerManagerConfigMapName,
		"instance", d.instance)

	return nil
}

// DiscoverControllerManager finds running controller manager information.
func (d *ControllerManagerDiscovery) DiscoverControllerManager(ctx context.Context) (*ControllerManagerInfo, error) {
	configMap, err := d.client.CoreV1().ConfigMaps(d.namespace).Get(ctx, ControllerManagerConfigMapName, metav1.GetOptions{})
	if errors.IsNotFound(err) {
		return nil, nil // No controller manager running
	}
	if err != nil {
		return nil, fmt.Errorf("failed to discover controller manager: %w", err)
	}

	return d.parseControllerManagerInfo(configMap)
}

// IsControllerRunning checks if a specific controller is running in the discovered controller manager.
func (d *ControllerManagerDiscovery) IsControllerRunning(ctx context.Context, controllerName string) (bool, *ControllerManagerInfo, error) {
	info, err := d.DiscoverControllerManager(ctx)
	if err != nil {
		return false, nil, err
	}
	if info == nil {
		return false, nil, nil // No controller manager running
	}

	// Check if the controller manager is actually accessible
	if !d.isControllerManagerHealthy(ctx, info) {
		return false, info, nil
	}

	// Check if the specific controller is enabled
	return slices.Contains(info.EnabledControllers, controllerName), info, nil
}

// ControllerManagerInfo contains discovered information about a running controller manager.
type ControllerManagerInfo struct {
	HealthPort         int       `json:"healthPort"`
	MetricsPort        int       `json:"metricsPort"`
	EnabledControllers []string  `json:"enabledControllers"`
	StartTime          time.Time `json:"startTime"`
	HealthEndpoint     string    `json:"healthEndpoint"`
	MetricsEndpoint    string    `json:"metricsEndpoint"`
}

// parseControllerManagerInfo converts ConfigMap data to ControllerManagerInfo.
func (d *ControllerManagerDiscovery) parseControllerManagerInfo(cm *corev1.ConfigMap) (*ControllerManagerInfo, error) {
	healthPort, err := strconv.Atoi(cm.Data["healthPort"])
	if err != nil {
		return nil, fmt.Errorf("invalid healthPort: %w", err)
	}

	metricsPort, err := strconv.Atoi(cm.Data["metricsPort"])
	if err != nil {
		return nil, fmt.Errorf("invalid metricsPort: %w", err)
	}

	startTime, err := time.Parse(time.RFC3339, cm.Data["startTime"])
	if err != nil {
		return nil, fmt.Errorf("invalid startTime: %w", err)
	}

	var controllers []string
	if controllersStr := cm.Data["enabledControllers"]; controllersStr != "" {
		controllers = strings.Split(controllersStr, ",")
	}

	return &ControllerManagerInfo{
		HealthPort:         healthPort,
		MetricsPort:        metricsPort,
		EnabledControllers: controllers,
		StartTime:          startTime,
		HealthEndpoint:     cm.Data["healthEndpoint"],
		MetricsEndpoint:    cm.Data["metricsEndpoint"],
	}, nil
}

// isControllerManagerHealthy checks if the controller manager is responding to health checks.
func (d *ControllerManagerDiscovery) isControllerManagerHealthy(ctx context.Context, info *ControllerManagerInfo) bool {
	client := &http.Client{Timeout: 2 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, info.HealthEndpoint, http.NoBody)
	if err != nil {
		return false
	}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// ensureNamespace creates the rdd-system namespace if it doesn't exist.
func (d *ControllerManagerDiscovery) ensureNamespace(ctx context.Context) error {
	namespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: d.namespace,
			Labels: map[string]string{
				"app.kubernetes.io/name": "rancher-desktop-daemon",
			},
		},
	}

	_, err := d.client.CoreV1().Namespaces().Create(ctx, namespace, metav1.CreateOptions{})
	if errors.IsAlreadyExists(err) {
		return nil // Namespace already exists, that's fine
	}
	return err
}
