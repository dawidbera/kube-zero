package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// PolicyPhase describes the current operational phase of a ZeroPolicy.
type PolicyPhase string

const (
	// PhaseActive indicates the managed workload is scaled up and running.
	PhaseActive PolicyPhase = "Active"
	// PhaseSleeping indicates the managed workload is scaled down to 0 replicas.
	PhaseSleeping PolicyPhase = "Sleeping"
	// PhaseWaking indicates the managed workload is currently in the process of scaling up.
	PhaseWaking PolicyPhase = "Waking"
	// PhaseFailed indicates an error occurred during reconcile or scale operation.
	PhaseFailed PolicyPhase = "Failed"
)

// TargetReference specifies the Kubernetes workload targeted by this ZeroPolicy.
type TargetReference struct {
	// APIVersion of the target resource (e.g. apps/v1).
	APIVersion string `json:"apiVersion,omitempty"`
	// Kind of the target resource (e.g. Deployment).
	Kind string `json:"kind"`
	// Name of the target resource within the same namespace.
	Name string `json:"name"`
}

// ScheduleSpec defines recurring cron windows for sleeping and waking.
type ScheduleSpec struct {
	// SleepCron is a standard cron expression specifying when the workload should scale down to 0.
	SleepCron string `json:"sleepCron,omitempty"`
	// WakeCron is a standard cron expression specifying when the workload should restore desired replicas.
	WakeCron string `json:"wakeCron,omitempty"`
	// Timezone specifies the IANA timezone for cron evaluation (default UTC).
	Timezone string `json:"timezone,omitempty"`
}

// InactivitySpec configures dynamic idle scale-to-zero.
type InactivitySpec struct {
	// Enabled determines if idle timeout auto-sleep is active.
	Enabled bool `json:"enabled"`
	// IdleTimeout specifies the duration of zero traffic before sleeping (e.g. "30m").
	IdleTimeout string `json:"idleTimeout,omitempty"`
}

// WakeOnTrafficSpec configures the HTTP interceptor reverse proxy behavior.
type WakeOnTrafficSpec struct {
	// Enabled determines if incoming traffic to a sleeping workload triggers an automatic scale-up.
	Enabled bool `json:"enabled"`
	// ServicePort is the port exposed on the Kubernetes Service.
	ServicePort int32 `json:"servicePort,omitempty"`
	// TargetPort is the container port exposed by the target workload.
	TargetPort int32 `json:"targetPort,omitempty"`
	// ReadinessTimeout specifies maximum wait time for pod readiness before returning 504 (default: "45s").
	ReadinessTimeout string `json:"readinessTimeout,omitempty"`
	// MaxBufferedBodyBytes sets the maximum request payload size held in memory during cold start.
	MaxBufferedBodyBytes int64 `json:"maxBufferedBodyBytes,omitempty"`
}

// CostProfileSpec holds pricing parameters to compute dollar savings.
type CostProfileSpec struct {
	// HourlyCpuCoreCostUsd is the estimated dollar cost per vCPU core per hour.
	HourlyCpuCoreCostUsd float64 `json:"hourlyCpuCoreCostUsd,omitempty"`
	// HourlyMemoryGbCostUsd is the estimated dollar cost per GB of memory per hour.
	HourlyMemoryGbCostUsd float64 `json:"hourlyMemoryGbCostUsd,omitempty"`
}

// ZeroPolicySpec defines the desired state of ZeroPolicy.
type ZeroPolicySpec struct {
	// TargetRef references the deployment or statefulset to manage.
	TargetRef TargetReference `json:"targetRef"`
	// Schedule defines cron-based sleep and wake windows.
	Schedule *ScheduleSpec `json:"schedule,omitempty"`
	// Inactivity defines dynamic idle scale-down parameters.
	Inactivity *InactivitySpec `json:"inactivity,omitempty"`
	// WakeOnTraffic defines HTTP proxy interception settings.
	WakeOnTraffic *WakeOnTrafficSpec `json:"wakeOnTraffic,omitempty"`
	// CostProfile defines pricing parameters for FinOps ROI calculations.
	CostProfile *CostProfileSpec `json:"costProfile,omitempty"`
}

// ZeroPolicyStatus defines the observed state of ZeroPolicy.
type ZeroPolicyStatus struct {
	// Phase is the current operational state of the policy.
	Phase PolicyPhase `json:"phase,omitempty"`
	// OriginalReplicas preserves the replica count before downscaling.
	OriginalReplicas int32 `json:"originalReplicas,omitempty"`
	// LastActivityTime records the last time traffic was detected.
	LastActivityTime *metav1.Time `json:"lastActivityTime,omitempty"`
	// LastSleepTime records when the workload was scaled down to 0.
	LastSleepTime *metav1.Time `json:"lastSleepTime,omitempty"`
	// AccumulatedSleepMinutes is the cumulative sleeping duration in minutes.
	AccumulatedSleepMinutes int64 `json:"accumulatedSleepMinutes,omitempty"`
	// TotalSavedCostUsd is the cumulative estimated cost saved in USD.
	TotalSavedCostUsd float64 `json:"totalSavedCostUsd,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// ZeroPolicy is the Schema for the zeropolicies API.
type ZeroPolicy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ZeroPolicySpec   `json:"spec,omitempty"`
	Status ZeroPolicyStatus `json:"status,omitempty"`
}

// IsSleeping returns true if the policy is currently in the Sleeping phase.
func (p *ZeroPolicy) IsSleeping() bool {
	return p.Status.Phase == PhaseSleeping
}

// ShouldWakeOnTraffic returns true if wakeOnTraffic is configured and enabled.
func (p *ZeroPolicy) ShouldWakeOnTraffic() bool {
	return p.Spec.WakeOnTraffic != nil && p.Spec.WakeOnTraffic.Enabled
}

// ZeroPolicyList contains a list of ZeroPolicy.
type ZeroPolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ZeroPolicy `json:"items"`
}
