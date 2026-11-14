package v1alpha1

import (
	"encoding/json"
	"testing"
)

// TestZeroPolicy_Serialization verifies JSON marshaling and helper methods on ZeroPolicy.
func TestZeroPolicy_Serialization(t *testing.T) {
	policy := ZeroPolicy{
		Spec: ZeroPolicySpec{
			TargetRef: TargetReference{
				Kind: "Deployment",
				Name: "payment-api",
			},
			WakeOnTraffic: &WakeOnTrafficSpec{
				Enabled:     true,
				ServicePort: 8080,
				TargetPort:  8080,
			},
			CostProfile: &CostProfileSpec{
				HourlyCpuCoreCostUsd:  0.04,
				HourlyMemoryGbCostUsd: 0.005,
			},
		},
		Status: ZeroPolicyStatus{
			Phase:            PhaseSleeping,
			OriginalReplicas: 3,
		},
	}

	if !policy.IsSleeping() {
		t.Errorf("expected policy to be sleeping, got %s", policy.Status.Phase)
	}

	if !policy.ShouldWakeOnTraffic() {
		t.Errorf("expected ShouldWakeOnTraffic to return true")
	}

	data, err := json.Marshal(policy)
	if err != nil {
		t.Fatalf("failed to marshal ZeroPolicy: %v", err)
	}

	var unmarshaled ZeroPolicy
	if err := json.Unmarshal(data, &unmarshaled); err != nil {
		t.Fatalf("failed to unmarshal ZeroPolicy: %v", err)
	}

	if unmarshaled.Spec.TargetRef.Name != "payment-api" {
		t.Errorf("expected target name 'payment-api', got '%s'", unmarshaled.Spec.TargetRef.Name)
	}
}
