package controller

import (
	"context"
	"testing"
	"time"

	"github.com/kube-zero/kube-zero/api/v1alpha1"
	"github.com/kube-zero/kube-zero/internal/finops"
	"github.com/prometheus/client_golang/prometheus"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// TestZeroPolicyReconciler_SleepAndWakeCycle tests a complete sleep and wake reconciliation cycle.
func TestZeroPolicyReconciler_SleepAndWakeCycle(t *testing.T) {
	ctx := context.Background()
	three := int32(3)

	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "billing-service",
			Namespace: "prod",
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &three,
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "billing",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("500m"),
									corev1.ResourceMemory: resource.MustParse("1Gi"),
								},
							},
						},
					},
				},
			},
		},
	}

	fakeClient := fake.NewSimpleClientset(deploy)
	reg := prometheus.NewRegistry()
	metrics := finops.NewMetrics(reg)

	reconciler := NewZeroPolicyReconciler(fakeClient, metrics, nil)

	policy := &v1alpha1.ZeroPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "billing-sleep-policy",
			Namespace: "prod",
		},
		Spec: v1alpha1.ZeroPolicySpec{
			TargetRef: v1alpha1.TargetReference{
				Kind: "Deployment",
				Name: "billing-service",
			},
			Schedule: &v1alpha1.ScheduleSpec{
				SleepCron: "0 19 * * 1-5", // 19:00 Mon-Fri
				WakeCron:  "0 7 * * 1-5",  // 07:00 Mon-Fri
				Timezone:  "UTC",
			},
		},
	}

	// 1. Reconcile at 19:00 (Sleep time)
	sleepTime := time.Date(2026, 11, 16, 19, 0, 0, 0, time.UTC)
	status, err := reconciler.Reconcile(ctx, policy, sleepTime)
	if err != nil {
		t.Fatalf("unexpected reconcile error on sleep: %v", err)
	}

	if status.Phase != v1alpha1.PhaseSleeping {
		t.Errorf("expected phase %s, got %s", v1alpha1.PhaseSleeping, status.Phase)
	}
	if status.OriginalReplicas != 3 {
		t.Errorf("expected originalReplicas 3, got %d", status.OriginalReplicas)
	}

	// Check updated deployment in fake API
	updatedDeploy, err := fakeClient.AppsV1().Deployments("prod").Get(ctx, "billing-service", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("failed to get deployment: %v", err)
	}
	if *updatedDeploy.Spec.Replicas != 0 {
		t.Errorf("expected deployment replicas to be 0, got %d", *updatedDeploy.Spec.Replicas)
	}
	if updatedDeploy.Annotations[AnnotationOriginalReplicas] != "3" {
		t.Errorf("expected annotation original-replicas to be '3', got '%s'", updatedDeploy.Annotations[AnnotationOriginalReplicas])
	}

	// 2. Reconcile at 07:00 next morning (12 hours later) -> Wake time
	policy.Status = *status
	wakeTime := sleepTime.Add(12 * time.Hour)
	wakeStatus, err := reconciler.Reconcile(ctx, policy, wakeTime)
	if err != nil {
		t.Fatalf("unexpected reconcile error on wake: %v", err)
	}

	if wakeStatus.Phase != v1alpha1.PhaseActive {
		t.Errorf("expected phase %s, got %s", v1alpha1.PhaseActive, wakeStatus.Phase)
	}
	if wakeStatus.AccumulatedSleepMinutes != 720 {
		t.Errorf("expected 720 sleep minutes, got %d", wakeStatus.AccumulatedSleepMinutes)
	}
	if wakeStatus.TotalSavedCostUsd <= 0 {
		t.Errorf("expected saved cost > 0 USD, got %f", wakeStatus.TotalSavedCostUsd)
	}

	// Check restored deployment in fake API
	restoredDeploy, err := fakeClient.AppsV1().Deployments("prod").Get(ctx, "billing-service", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("failed to get restored deployment: %v", err)
	}
	if *restoredDeploy.Spec.Replicas != 3 {
		t.Errorf("expected deployment replicas restored to 3, got %d", *restoredDeploy.Spec.Replicas)
	}
}
