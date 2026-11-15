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

// TestZeroPolicyReconciler_ServiceTrafficSwitchingCycle tests redirecting Service routing to wake-proxy on sleep and restoring it on wake.
func TestZeroPolicyReconciler_ServiceTrafficSwitchingCycle(t *testing.T) {
	ctx := context.Background()
	three := int32(3)

	podLabels := map[string]string{
		"app": "orders-service",
	}

	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "orders-service",
			Namespace: "dev",
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &three,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: podLabels,
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "orders",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("250m"),
									corev1.ResourceMemory: resource.MustParse("512Mi"),
								},
							},
						},
					},
				},
			},
		},
	}

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "orders-service",
			Namespace: "dev",
		},
		Spec: corev1.ServiceSpec{
			Selector: podLabels,
			Ports: []corev1.ServicePort{
				{
					Port: 8080,
				},
			},
		},
	}

	fakeClient := fake.NewSimpleClientset(deploy, svc)
	reg := prometheus.NewRegistry()
	metrics := finops.NewMetrics(reg)
	reconciler := NewZeroPolicyReconciler(fakeClient, metrics, nil)

	policy := &v1alpha1.ZeroPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "orders-policy",
			Namespace: "dev",
		},
		Spec: v1alpha1.ZeroPolicySpec{
			TargetRef: v1alpha1.TargetReference{
				Kind: "Deployment",
				Name: "orders-service",
			},
			WakeOnTraffic: &v1alpha1.WakeOnTrafficSpec{
				Enabled: true,
			},
			Schedule: &v1alpha1.ScheduleSpec{
				SleepCron: "0 19 * * 1-5",
				WakeCron:  "0 7 * * 1-5",
				Timezone:  "UTC",
			},
		},
	}

	// 1. Reconcile at sleep time (19:00 UTC)
	sleepTime := time.Date(2026, 11, 16, 19, 0, 0, 0, time.UTC)
	status, err := reconciler.Reconcile(ctx, policy, sleepTime)
	if err != nil {
		t.Fatalf("unexpected reconcile error on sleep: %v", err)
	}

	if status.Phase != v1alpha1.PhaseSleeping {
		t.Fatalf("expected phase Sleeping, got %s", status.Phase)
	}

	// Verify Service was redirected to wake-proxy
	updatedSvc, err := fakeClient.CoreV1().Services("dev").Get(ctx, "orders-service", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("failed to get service: %v", err)
	}
	if updatedSvc.Spec.Selector[LabelWakeProxyKey] != LabelWakeProxyValue {
		t.Errorf("expected service selector to point to wake-proxy, got %v", updatedSvc.Spec.Selector)
	}
	if updatedSvc.Annotations[AnnotationOriginalSelector] == "" {
		t.Errorf("expected original-selector annotation to be populated")
	}
	if updatedSvc.Annotations[AnnotationTargetDeployment] != "orders-service" {
		t.Errorf("expected target-deployment annotation orders-service, got %s", updatedSvc.Annotations[AnnotationTargetDeployment])
	}

	// 2. Reconcile at wake time (07:00 UTC next morning)
	policy.Status = *status
	wakeTime := sleepTime.Add(12 * time.Hour)
	wakeStatus, err := reconciler.Reconcile(ctx, policy, wakeTime)
	if err != nil {
		t.Fatalf("unexpected reconcile error on wake: %v", err)
	}

	if wakeStatus.Phase != v1alpha1.PhaseActive {
		t.Fatalf("expected phase Active, got %s", wakeStatus.Phase)
	}

	// Verify Service selector was restored to original pod labels
	restoredSvc, err := fakeClient.CoreV1().Services("dev").Get(ctx, "orders-service", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("failed to get restored service: %v", err)
	}
	if restoredSvc.Spec.Selector["app"] != "orders-service" {
		t.Errorf("expected restored service selector to match original pod label, got %v", restoredSvc.Spec.Selector)
	}
	if _, ok := restoredSvc.Annotations[AnnotationOriginalSelector]; ok {
		t.Errorf("expected original-selector annotation to be removed after wake")
	}
	if _, ok := restoredSvc.Annotations[AnnotationTargetDeployment]; ok {
		t.Errorf("expected target-deployment annotation to be removed after wake")
	}
}
