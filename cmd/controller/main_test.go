package main

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/kube-zero/kube-zero/api/v1alpha1"
	"github.com/kube-zero/kube-zero/internal/controller"
	"github.com/kube-zero/kube-zero/internal/finops"
	"github.com/kube-zero/kube-zero/internal/k8s"
	"github.com/prometheus/client_golang/prometheus"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"
)

func TestReconcileAllPolicies(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	// Create fake deployment with 3 replicas
	replicas := int32(3)
	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "orders-service",
			Namespace: "production",
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "orders",
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

	k8sClient := fake.NewSimpleClientset(deploy)

	// Create a ZeroPolicy configured to sleep at 19:00 Mon-Fri
	policy := &v1alpha1.ZeroPolicy{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "finops.kubezero.io/v1alpha1",
			Kind:       "ZeroPolicy",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "orders-sleep-policy",
			Namespace: "production",
		},
		Spec: v1alpha1.ZeroPolicySpec{
			TargetRef: v1alpha1.TargetReference{
				Kind: "Deployment",
				Name: "orders-service",
			},
			Schedule: &v1alpha1.ScheduleSpec{
				SleepCron: "0 19 * * 1-5",
				WakeCron:  "0 7 * * 1-5",
				Timezone:  "UTC",
			},
		},
	}

	u, err := k8s.ZeroPolicyToUnstructured(policy)
	if err != nil {
		t.Fatalf("failed to convert policy to unstructured: %v", err)
	}

	scheme := runtime.NewScheme()
	gvrToListKind := map[schema.GroupVersionResource]string{
		k8s.ZeroPolicyGVR: "ZeroPolicyList",
	}
	dynClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, gvrToListKind, u)

	reg := prometheus.NewRegistry()
	metrics := finops.NewMetrics(reg)
	reconciler := controller.NewZeroPolicyReconciler(k8sClient, metrics, logger)

	// Evaluate at 19:00 UTC on a Wednesday (sleepCron trigger)
	evalTime := time.Date(2026, 11, 18, 19, 0, 0, 0, time.UTC)

	reconcileAllPolicies(ctx, dynClient, reconciler, "production", evalTime, logger)

	// Verify deployment was scaled to 0
	updatedDeploy, err := k8sClient.AppsV1().Deployments("production").Get(ctx, "orders-service", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("failed to get deployment: %v", err)
	}
	if *updatedDeploy.Spec.Replicas != 0 {
		t.Errorf("expected replicas to be 0, got %d", *updatedDeploy.Spec.Replicas)
	}
	if updatedDeploy.Annotations[controller.AnnotationOriginalReplicas] != "3" {
		t.Errorf("expected original-replicas annotation 3, got %s", updatedDeploy.Annotations[controller.AnnotationOriginalReplicas])
	}

	// Verify ZeroPolicy status updated to Sleeping
	policies, err := k8s.ListZeroPolicies(ctx, dynClient, "production")
	if err != nil {
		t.Fatalf("failed to list policies: %v", err)
	}
	if len(policies) != 1 {
		t.Fatalf("expected 1 policy, got %d", len(policies))
	}
	if policies[0].Status.Phase != v1alpha1.PhaseSleeping {
		t.Errorf("expected status phase Sleeping, got %s", policies[0].Status.Phase)
	}
	if policies[0].Status.OriginalReplicas != 3 {
		t.Errorf("expected original replicas 3 in status, got %d", policies[0].Status.OriginalReplicas)
	}
}
