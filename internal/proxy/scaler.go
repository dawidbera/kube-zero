package proxy

import (
	"context"
	"fmt"
	"sync"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// Scaler defines operations required by the Wake-on-Traffic proxy to inspect and scale workloads.
type Scaler interface {
	// GetDeploymentState returns the desired replicas and currently ready replicas for a deployment.
	GetDeploymentState(ctx context.Context, namespace, name string) (desired int32, ready int32, err error)

	// ScaleUp patches the deployment replica count to targetReplicas.
	ScaleUp(ctx context.Context, namespace, name string, targetReplicas int32) error
}

// KubernetesScaler implements Scaler using the official Kubernetes clientset.
type KubernetesScaler struct {
	client kubernetes.Interface
}

// NewKubernetesScaler creates a Scaler backed by a Kubernetes clientset.
func NewKubernetesScaler(client kubernetes.Interface) *KubernetesScaler {
	return &KubernetesScaler{client: client}
}

// GetDeploymentState fetches the current replica counts from the Kubernetes API.
func (k *KubernetesScaler) GetDeploymentState(ctx context.Context, namespace, name string) (int32, int32, error) {
	deploy, err := k.client.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return 0, 0, fmt.Errorf("failed to get deployment %s/%s: %w", namespace, name, err)
	}

	desired := int32(1)
	if deploy.Spec.Replicas != nil {
		desired = *deploy.Spec.Replicas
	}

	return desired, deploy.Status.ReadyReplicas, nil
}

// ScaleUp patches the scale subresource of the target deployment.
func (k *KubernetesScaler) ScaleUp(ctx context.Context, namespace, name string, targetReplicas int32) error {
	scale, err := k.client.AppsV1().Deployments(namespace).GetScale(ctx, name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get scale for %s/%s: %w", namespace, name, err)
	}

	scale.Spec.Replicas = targetReplicas
	_, err = k.client.AppsV1().Deployments(namespace).UpdateScale(ctx, name, scale, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update scale for %s/%s: %w", namespace, name, err)
	}

	return nil
}

// MockScaler provides an in-memory Scaler implementation for automated testing and local development.
type MockScaler struct {
	mu            sync.Mutex
	desired       int32
	ready         int32
	scaleUpCalled bool
}

// NewMockScaler creates a thread-safe MockScaler with initial desired and ready replicas.
func NewMockScaler(desired, ready int32) *MockScaler {
	return &MockScaler{
		desired: desired,
		ready:   ready,
	}
}

// GetDeploymentState returns the simulated replica counts.
func (m *MockScaler) GetDeploymentState(ctx context.Context, namespace, name string) (int32, int32, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.desired, m.ready, nil
}

// ScaleUp records the scale-up request and updates desired replicas.
func (m *MockScaler) ScaleUp(ctx context.Context, namespace, name string, targetReplicas int32) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.scaleUpCalled = true
	m.desired = targetReplicas
	return nil
}

// SetReady safely modifies the simulated ready count (e.g. to simulate pod startup during tests).
func (m *MockScaler) SetReady(ready int32) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ready = ready
}

// WasScaleUpCalled returns whether ScaleUp has been invoked.
func (m *MockScaler) WasScaleUpCalled() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.scaleUpCalled
}
