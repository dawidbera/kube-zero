package k8s

import (
	"context"
	"testing"
	"time"

	"github.com/kube-zero/kube-zero/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

func TestUnstructuredRoundtrip(t *testing.T) {
	now := metav1.NewTime(time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC))
	origPolicy := &v1alpha1.ZeroPolicy{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "finops.kubezero.io/v1alpha1",
			Kind:       "ZeroPolicy",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-policy",
			Namespace: "dev",
		},
		Spec: v1alpha1.ZeroPolicySpec{
			TargetRef: v1alpha1.TargetReference{
				Kind: "Deployment",
				Name: "payment-api",
			},
			Schedule: &v1alpha1.ScheduleSpec{
				SleepCron: "0 19 * * 1-5",
				WakeCron:  "0 7 * * 1-5",
				Timezone:  "UTC",
			},
		},
		Status: v1alpha1.ZeroPolicyStatus{
			Phase:                   v1alpha1.PhaseSleeping,
			OriginalReplicas:        2,
			AccumulatedSleepMinutes: 120,
			TotalSavedCostUsd:       4.52,
			LastSleepTime:           &now,
		},
	}

	unstr, err := ZeroPolicyToUnstructured(origPolicy)
	if err != nil {
		t.Fatalf("ZeroPolicyToUnstructured failed: %v", err)
	}

	converted, err := UnstructuredToZeroPolicy(unstr)
	if err != nil {
		t.Fatalf("UnstructuredToZeroPolicy failed: %v", err)
	}

	if converted.Name != origPolicy.Name {
		t.Errorf("expected Name %s, got %s", origPolicy.Name, converted.Name)
	}
	if converted.Spec.TargetRef.Name != origPolicy.Spec.TargetRef.Name {
		t.Errorf("expected TargetRef.Name %s, got %s", origPolicy.Spec.TargetRef.Name, converted.Spec.TargetRef.Name)
	}
	if converted.Status.Phase != origPolicy.Status.Phase {
		t.Errorf("expected Phase %s, got %s", origPolicy.Status.Phase, converted.Status.Phase)
	}
	if converted.Status.OriginalReplicas != origPolicy.Status.OriginalReplicas {
		t.Errorf("expected OriginalReplicas %d, got %d", origPolicy.Status.OriginalReplicas, converted.Status.OriginalReplicas)
	}
}

func TestListAndUpdateZeroPoliciesWithFakeClient(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()

	policy := &v1alpha1.ZeroPolicy{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "finops.kubezero.io/v1alpha1",
			Kind:       "ZeroPolicy",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "api-policy",
			Namespace: "staging",
		},
		Spec: v1alpha1.ZeroPolicySpec{
			TargetRef: v1alpha1.TargetReference{
				Kind: "Deployment",
				Name: "catalog-service",
			},
		},
		Status: v1alpha1.ZeroPolicyStatus{
			Phase: v1alpha1.PhaseActive,
		},
	}

	u, err := ZeroPolicyToUnstructured(policy)
	if err != nil {
		t.Fatalf("failed to prepare unstructured policy: %v", err)
	}

	gvrToListKind := map[schema.GroupVersionResource]string{
		ZeroPolicyGVR: "ZeroPolicyList",
	}
	dynClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, gvrToListKind, u)

	// Test ListZeroPolicies
	policies, err := ListZeroPolicies(ctx, dynClient, "staging")
	if err != nil {
		t.Fatalf("ListZeroPolicies failed: %v", err)
	}
	if len(policies) != 1 {
		t.Fatalf("expected 1 policy, got %d", len(policies))
	}
	if policies[0].Name != "api-policy" {
		t.Errorf("expected policy name api-policy, got %s", policies[0].Name)
	}

	// Test UpdateZeroPolicyStatus
	newStatus := &v1alpha1.ZeroPolicyStatus{
		Phase:                   v1alpha1.PhaseSleeping,
		OriginalReplicas:        3,
		AccumulatedSleepMinutes: 60,
		TotalSavedCostUsd:       1.25,
	}

	if err := UpdateZeroPolicyStatus(ctx, dynClient, "staging", "api-policy", newStatus); err != nil {
		t.Fatalf("UpdateZeroPolicyStatus failed: %v", err)
	}

	// Verify status updated
	updatedList, err := ListZeroPolicies(ctx, dynClient, "staging")
	if err != nil {
		t.Fatalf("ListZeroPolicies after update failed: %v", err)
	}
	if len(updatedList) != 1 {
		t.Fatalf("expected 1 policy, got %d", len(updatedList))
	}
	if updatedList[0].Status.Phase != v1alpha1.PhaseSleeping {
		t.Errorf("expected phase Sleeping, got %s", updatedList[0].Status.Phase)
	}
	if updatedList[0].Status.TotalSavedCostUsd != 1.25 {
		t.Errorf("expected TotalSavedCostUsd 1.25, got %f", updatedList[0].Status.TotalSavedCostUsd)
	}
}

func TestNilAndEmptyValidations(t *testing.T) {
	ctx := context.Background()

	if _, err := UnstructuredToZeroPolicy(nil); err == nil {
		t.Error("expected error on nil UnstructuredToZeroPolicy")
	}
	if _, err := ZeroPolicyToUnstructured(nil); err == nil {
		t.Error("expected error on nil ZeroPolicyToUnstructured")
	}
	if _, err := ListZeroPolicies(ctx, nil, "default"); err == nil {
		t.Error("expected error on nil dynamic client in ListZeroPolicies")
	}
	if err := UpdateZeroPolicyStatus(ctx, nil, "default", "pol", &v1alpha1.ZeroPolicyStatus{}); err == nil {
		t.Error("expected error on nil dynamic client in UpdateZeroPolicyStatus")
	}
	scheme := runtime.NewScheme()
	dynClient := dynamicfake.NewSimpleDynamicClient(scheme)
	if err := UpdateZeroPolicyStatus(ctx, dynClient, "default", "", &v1alpha1.ZeroPolicyStatus{}); err == nil {
		t.Error("expected error on empty name in UpdateZeroPolicyStatus")
	}
	if err := UpdateZeroPolicyStatus(ctx, dynClient, "default", "pol", nil); err == nil {
		t.Error("expected error on nil status in UpdateZeroPolicyStatus")
	}
}
