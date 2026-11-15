// Package k8s provides shared Kubernetes client initializers and custom resource helpers for KubeZero.
package k8s

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/kube-zero/kube-zero/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// ZeroPolicyGVR defines the GroupVersionResource for KubeZero's ZeroPolicy custom resource.
var ZeroPolicyGVR = schema.GroupVersionResource{
	Group:    "finops.kubezero.io",
	Version:  "v1alpha1",
	Resource: "zeropolicies",
}

// GetRESTConfig builds a Kubernetes REST client configuration.
// If kubeconfigPath is non-empty, it parses the file at that path; otherwise, it attempts in-cluster credentials.
//
// Parameters:
//   - kubeconfigPath: Absolute or relative filesystem path to kubeconfig file, or empty string for in-cluster.
//
// Returns:
//   - A valid *rest.Config pointer or an error if configuration resolution fails.
func GetRESTConfig(kubeconfigPath string) (*rest.Config, error) {
	if kubeconfigPath != "" {
		return clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	}
	return rest.InClusterConfig()
}

// NewClientset constructs a typed Kubernetes Clientset interface.
//
// Parameters:
//   - kubeconfigPath: Optional filesystem path to kubeconfig file.
//
// Returns:
//   - kubernetes.Interface for standard K8s resources (Deployments, Services, etc.) or an error.
func NewClientset(kubeconfigPath string) (kubernetes.Interface, error) {
	cfg, err := GetRESTConfig(kubeconfigPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load kubeconfig: %w", err)
	}
	return kubernetes.NewForConfig(cfg)
}

// NewDynamicClient constructs a dynamic Kubernetes client interface for working with CRDs.
//
// Parameters:
//   - kubeconfigPath: Optional filesystem path to kubeconfig file.
//
// Returns:
//   - dynamic.Interface for custom resources, or an error if creation fails.
func NewDynamicClient(kubeconfigPath string) (dynamic.Interface, error) {
	cfg, err := GetRESTConfig(kubeconfigPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load kubeconfig: %w", err)
	}
	return dynamic.NewForConfig(cfg)
}

// UnstructuredToZeroPolicy unmarshals a Kubernetes Unstructured object into a strongly typed ZeroPolicy struct.
//
// Parameters:
//   - u: Unstructured Kubernetes resource.
//
// Returns:
//   - Typed *v1alpha1.ZeroPolicy or an error if conversion fails.
func UnstructuredToZeroPolicy(u *unstructured.Unstructured) (*v1alpha1.ZeroPolicy, error) {
	if u == nil {
		return nil, errors.New("unstructured object is nil")
	}

	raw, err := u.MarshalJSON()
	if err != nil {
		return nil, fmt.Errorf("failed to marshal unstructured object: %w", err)
	}

	var policy v1alpha1.ZeroPolicy
	if err := json.Unmarshal(raw, &policy); err != nil {
		return nil, fmt.Errorf("failed to unmarshal JSON to ZeroPolicy: %w", err)
	}

	return &policy, nil
}

// ZeroPolicyToUnstructured marshals a strongly typed ZeroPolicy struct into a Kubernetes Unstructured object.
//
// Parameters:
//   - policy: Typed ZeroPolicy struct.
//
// Returns:
//   - *unstructured.Unstructured representation or an error if conversion fails.
func ZeroPolicyToUnstructured(policy *v1alpha1.ZeroPolicy) (*unstructured.Unstructured, error) {
	if policy == nil {
		return nil, errors.New("policy is nil")
	}

	raw, err := json.Marshal(policy)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal ZeroPolicy: %w", err)
	}

	var obj map[string]interface{}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, fmt.Errorf("failed to unmarshal ZeroPolicy to object map: %w", err)
	}

	return &unstructured.Unstructured{Object: obj}, nil
}

// ListZeroPolicies fetches all ZeroPolicy resources via the dynamic client.
//
// Parameters:
//   - ctx: Context for cancellation and deadline control.
//   - dyn: Dynamic Kubernetes client interface.
//   - namespace: Target namespace to filter policies, or empty string to list across all namespaces.
//
// Returns:
//   - Slice of parsed ZeroPolicy instances or an error.
func ListZeroPolicies(ctx context.Context, dyn dynamic.Interface, namespace string) ([]v1alpha1.ZeroPolicy, error) {
	if dyn == nil {
		return nil, errors.New("dynamic client must not be nil")
	}

	var res dynamic.ResourceInterface
	if namespace != "" {
		res = dyn.Resource(ZeroPolicyGVR).Namespace(namespace)
	} else {
		res = dyn.Resource(ZeroPolicyGVR)
	}

	list, err := res.List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list ZeroPolicy resources: %w", err)
	}

	policies := make([]v1alpha1.ZeroPolicy, 0, len(list.Items))
	for i := range list.Items {
		policy, err := UnstructuredToZeroPolicy(&list.Items[i])
		if err != nil {
			return nil, fmt.Errorf("failed to convert item %d to ZeroPolicy: %w", i, err)
		}
		policies = append(policies, *policy)
	}

	return policies, nil
}

// UpdateZeroPolicyStatus updates the status subresource of a ZeroPolicy custom resource.
//
// Parameters:
//   - ctx: Context for cancellation and deadline control.
//   - dyn: Dynamic Kubernetes client interface.
//   - namespace: Namespace of the target ZeroPolicy.
//   - name: Name of the target ZeroPolicy.
//   - status: New status object to persist.
//
// Returns:
//   - Error if status update fails.
func UpdateZeroPolicyStatus(ctx context.Context, dyn dynamic.Interface, namespace, name string, status *v1alpha1.ZeroPolicyStatus) error {
	if dyn == nil {
		return errors.New("dynamic client must not be nil")
	}
	if name == "" {
		return errors.New("policy name must not be empty")
	}
	if status == nil {
		return errors.New("status must not be nil")
	}

	res := dyn.Resource(ZeroPolicyGVR).Namespace(namespace)
	u, err := res.Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to fetch ZeroPolicy %s/%s for status update: %w", namespace, name, err)
	}

	rawStatus, err := json.Marshal(status)
	if err != nil {
		return fmt.Errorf("failed to marshal ZeroPolicyStatus: %w", err)
	}

	var statusMap map[string]interface{}
	if err := json.Unmarshal(rawStatus, &statusMap); err != nil {
		return fmt.Errorf("failed to convert status to map: %w", err)
	}

	u.Object["status"] = statusMap
	if _, err := res.UpdateStatus(ctx, u, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("failed to update status subresource for ZeroPolicy %s/%s: %w", namespace, name, err)
	}

	return nil
}
