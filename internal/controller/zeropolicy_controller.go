package controller

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/kube-zero/kube-zero/api/v1alpha1"
	"github.com/kube-zero/kube-zero/internal/finops"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const (
	// AnnotationOriginalReplicas stores the workload's replica count before scaling down.
	AnnotationOriginalReplicas = "finops.kubezero.io/original-replicas"
	// AnnotationManagedBy denotes KubeZero ownership.
	AnnotationManagedBy = "finops.kubezero.io/managed-by"
)

// ZeroPolicyReconciler executes reconciliation cycles for ZeroPolicy resources against Kubernetes deployments.
type ZeroPolicyReconciler struct {
	client  kubernetes.Interface
	metrics *finops.Metrics
	logger  *slog.Logger
}

// NewZeroPolicyReconciler initializes a ZeroPolicyReconciler.
func NewZeroPolicyReconciler(client kubernetes.Interface, metrics *finops.Metrics, logger *slog.Logger) *ZeroPolicyReconciler {
	if logger == nil {
		logger = slog.Default()
	}
	return &ZeroPolicyReconciler{
		client:  client,
		metrics: metrics,
		logger:  logger,
	}
}

// Reconcile evaluates a single ZeroPolicy against its target deployment and reconciles replica counts and savings.
//
// Parameters:
//   - ctx: Context for API calls.
//   - policy: The ZeroPolicy object to evaluate.
//   - now: Current evaluation timestamp.
//
// Returns the updated ZeroPolicyStatus and any error encountered.
func (r *ZeroPolicyReconciler) Reconcile(ctx context.Context, policy *v1alpha1.ZeroPolicy, now time.Time) (*v1alpha1.ZeroPolicyStatus, error) {
	namespace := policy.Namespace
	targetName := policy.Spec.TargetRef.Name
	status := policy.Status

	deploy, err := r.client.AppsV1().Deployments(namespace).Get(ctx, targetName, metav1.GetOptions{})
	if err != nil {
		status.Phase = v1alpha1.PhaseFailed
		return &status, fmt.Errorf("failed to fetch target deployment %s/%s: %w", namespace, targetName, err)
	}

	currentReplicas := int32(1)
	if deploy.Spec.Replicas != nil {
		currentReplicas = *deploy.Spec.Replicas
	}

	decision, err := EvaluateSchedule(policy.Spec.Schedule, now)
	if err != nil {
		r.logger.Error("schedule evaluation error", "policy", policy.Name, "err", err)
	}

	var lastAct *time.Time
	if status.LastActivityTime != nil {
		t := status.LastActivityTime.Time
		lastAct = &t
	}
	isIdle, err := IsInactive(policy.Spec.Inactivity, lastAct, now)
	if err != nil {
		r.logger.Error("inactivity check error", "policy", policy.Name, "err", err)
	}

	shouldSleep := decision == DecisionSleep || isIdle
	shouldWake := decision == DecisionActive

	// Case 1: Workload should scale to 0
	if shouldSleep && currentReplicas > 0 {
		r.logger.Info("scaling workload to zero", "namespace", namespace, "deployment", targetName, "previousReplicas", currentReplicas)

		// Preserve original replicas in deployment annotation
		if deploy.Annotations == nil {
			deploy.Annotations = make(map[string]string)
		}
		deploy.Annotations[AnnotationOriginalReplicas] = strconv.Itoa(int(currentReplicas))
		deploy.Annotations[AnnotationManagedBy] = "kubezero"

		zero := int32(0)
		deploy.Spec.Replicas = &zero

		if _, err := r.client.AppsV1().Deployments(namespace).Update(ctx, deploy, metav1.UpdateOptions{}); err != nil {
			status.Phase = v1alpha1.PhaseFailed
			return &status, fmt.Errorf("failed to scale deployment %s to zero: %w", targetName, err)
		}

		nowMeta := metav1.NewTime(now)
		status.Phase = v1alpha1.PhaseSleeping
		status.OriginalReplicas = currentReplicas
		status.LastSleepTime = &nowMeta

		if r.metrics != nil {
			r.metrics.SleepingDeployments.WithLabelValues(namespace, targetName).Set(1)
		}
		return &status, nil
	}

	// Case 2: Workload should wake up
	if shouldWake && currentReplicas == 0 {
		targetReplicas := int32(1)
		if orig, ok := deploy.Annotations[AnnotationOriginalReplicas]; ok {
			if parsed, err := strconv.Atoi(orig); err == nil && parsed > 0 {
				targetReplicas = int32(parsed)
			}
		} else if status.OriginalReplicas > 0 {
			targetReplicas = status.OriginalReplicas
		}

		r.logger.Info("restoring workload replicas", "namespace", namespace, "deployment", targetName, "targetReplicas", targetReplicas)

		deploy.Spec.Replicas = &targetReplicas
		if _, err := r.client.AppsV1().Deployments(namespace).Update(ctx, deploy, metav1.UpdateOptions{}); err != nil {
			status.Phase = v1alpha1.PhaseFailed
			return &status, fmt.Errorf("failed to restore deployment %s replicas: %w", targetName, err)
		}

		// Calculate savings during sleep period
		if status.LastSleepTime != nil {
			sleepDuration := now.Sub(status.LastSleepTime.Time)
			if sleepDuration > 0 {
				cpuCores, memBytes := extractPodResourceRequests(deploy)
				savings := finops.CalculateSavings(cpuCores, memBytes, targetReplicas, sleepDuration, policy.Spec.CostProfile)

				status.AccumulatedSleepMinutes += int64(sleepDuration.Minutes())
				status.TotalSavedCostUsd += savings.SavedUsd

				if r.metrics != nil {
					r.metrics.CostSavingsUsdTotal.WithLabelValues(namespace, targetName).Add(savings.SavedUsd)
				}
			}
		}

		status.Phase = v1alpha1.PhaseActive
		if r.metrics != nil {
			r.metrics.SleepingDeployments.WithLabelValues(namespace, targetName).Set(0)
		}
		return &status, nil
	}

	// Update phase if unchanged
	if currentReplicas == 0 {
		status.Phase = v1alpha1.PhaseSleeping
	} else {
		status.Phase = v1alpha1.PhaseActive
	}

	return &status, nil
}

// extractPodResourceRequests parses CPU cores and memory bytes requests across all containers in a deployment.
func extractPodResourceRequests(deploy *appsv1.Deployment) (float64, int64) {
	var totalCpuCores float64
	var totalMemoryBytes int64

	for _, container := range deploy.Spec.Template.Spec.Containers {
		if req, ok := container.Resources.Requests[corev1.ResourceCPU]; ok {
			totalCpuCores += float64(req.MilliValue()) / 1000.0
		}
		if req, ok := container.Resources.Requests[corev1.ResourceMemory]; ok {
			totalMemoryBytes += req.Value()
		}
	}

	// Defaults if no explicit requests configured (0.25 core, 512MB RAM)
	if totalCpuCores == 0 {
		totalCpuCores = 0.25
	}
	if totalMemoryBytes == 0 {
		totalMemoryBytes = 512 * 1024 * 1024
	}

	return totalCpuCores, totalMemoryBytes
}
