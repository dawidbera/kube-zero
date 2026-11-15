package proxy

import (
	"context"
	"fmt"
	"sync"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// Scaler defines operations required by the Wake-on-Traffic proxy to inspect and scale workloads.
type Scaler interface {
	// GetDeploymentState returns the desired replicas and currently ready replicas for a deployment.
	GetDeploymentState(ctx context.Context, namespace, name string) (desired int32, ready int32, err error)

	// ScaleUp patches the deployment replica count to targetReplicas.
	ScaleUp(ctx context.Context, namespace, name string, targetReplicas int32) error

	// GetReadyPodIP returns the IP of an active ready pod for the deployment, or empty string if none.
	GetReadyPodIP(ctx context.Context, namespace, name string) (string, error)
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

// GetReadyPodIP retrieves the direct IP address of a running, ready pod belonging to the target deployment.
func (k *KubernetesScaler) GetReadyPodIP(ctx context.Context, namespace, name string) (string, error) {
	deploy, err := k.client.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to get deployment %s/%s: %w", namespace, name, err)
	}

	podList, err := k.client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to list pods in namespace %s: %w", namespace, err)
	}

	for _, pod := range podList.Items {
		if pod.Status.Phase != corev1.PodRunning || pod.Status.PodIP == "" {
			continue
		}

		// Match deployment pod template labels
		isMatch := true
		for k, v := range deploy.Spec.Template.Labels {
			if pod.Labels[k] != v {
				isMatch = false
				break
			}
		}
		if !isMatch {
			continue
		}

		for _, cond := range pod.Status.Conditions {
			if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionTrue {
				return pod.Status.PodIP, nil
			}
		}
	}

	return "", nil
}

// MockScaler provides an in-memory Scaler implementation for automated testing and local development.
type MockScaler struct {
	mu            sync.Mutex
	desired       int32
	ready         int32
	readyPodIP    string
	scaleUpCalled bool
}

// NewMockScaler creates a thread-safe MockScaler with initial desired and ready replicas.
func NewMockScaler(desired, ready int32) *MockScaler {
	ip := ""
	if ready > 0 {
		ip = "127.0.0.1"
	}
	return &MockScaler{
		desired:    desired,
		ready:      ready,
		readyPodIP: ip,
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

// GetReadyPodIP returns the simulated ready pod IP address.
func (m *MockScaler) GetReadyPodIP(ctx context.Context, namespace, name string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ready > 0 {
		if m.readyPodIP != "" {
			return m.readyPodIP, nil
		}
		return "127.0.0.1", nil
	}
	return "", nil
}

// SetReady safely modifies the simulated ready count and ready pod IP.
func (m *MockScaler) SetReady(ready int32) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ready = ready
	if ready > 0 {
		m.readyPodIP = "127.0.0.1"
	} else {
		m.readyPodIP = ""
	}
}

// WasScaleUpCalled returns whether ScaleUp has been invoked.
func (m *MockScaler) WasScaleUpCalled() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.scaleUpCalled
}
