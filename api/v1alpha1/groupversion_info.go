// Package v1alpha1 contains API Schema definitions for the finops v1alpha1 API group.
// +kubebuilder:object:generate=true
// +groupName=finops.kubezero.io
package v1alpha1

import (
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var (
	// GroupVersion is group version used to register these objects.
	GroupVersion = schema.GroupVersion{Group: "finops.kubezero.io", Version: "v1alpha1"}
)
