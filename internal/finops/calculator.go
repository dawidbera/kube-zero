// Package finops provides cost calculation and telemetry metrics for KubeZero workloads.
package finops

import (
	"time"

	"github.com/kube-zero/kube-zero/api/v1alpha1"
)

const (
	// DefaultHourlyCpuCostUsd is the default cost per vCPU core hour (~AWS c6i.large rate).
	DefaultHourlyCpuCostUsd = 0.0416
	// DefaultHourlyMemoryGbCostUsd is the default cost per GB RAM hour.
	DefaultHourlyMemoryGbCostUsd = 0.0052
	// BytesInGigabyte represents the number of bytes in one gigabyte (binary 1024^3).
	BytesInGigabyte = 1024 * 1024 * 1024
)

// SavingsSummary holds the computed resource and financial savings.
type SavingsSummary struct {
	// Duration of sleep that yielded these savings.
	Duration time.Duration
	// SavedCpuCoreSeconds is the cumulative CPU core-seconds saved.
	SavedCpuCoreSeconds float64
	// SavedMemoryGbSeconds is the cumulative memory GB-seconds saved.
	SavedMemoryGbSeconds float64
	// SavedUsd is the total estimated financial savings in US dollars.
	SavedUsd float64
}

// CalculateSavings computes the avoided compute cost for a sleeping workload.
//
// Parameters:
//   - cpuCores: Total CPU requests across containers for a single pod (e.g. 0.5 = 500m).
//   - memoryBytes: Total memory requests across containers for a single pod in bytes.
//   - replicas: Number of replicas that were scaled to zero.
//   - duration: Time elapsed while the workload was scaled down.
//   - profile: Optional pricing profile overrides. If nil or zero, default cloud rates apply.
//
// Edge cases:
//   - If duration <= 0 or replicas <= 0, returns a zeroed SavingsSummary.
//   - Negative CPU or memory values are treated as 0.
func CalculateSavings(
	cpuCores float64,
	memoryBytes int64,
	replicas int32,
	duration time.Duration,
	profile *v1alpha1.CostProfileSpec,
) SavingsSummary {
	if duration <= 0 || replicas <= 0 {
		return SavingsSummary{Duration: duration}
	}

	if cpuCores < 0 {
		cpuCores = 0
	}
	if memoryBytes < 0 {
		memoryBytes = 0
	}

	totalCpuCores := cpuCores * float64(replicas)
	totalMemoryGb := (float64(memoryBytes) / float64(BytesInGigabyte)) * float64(replicas)

	seconds := duration.Seconds()
	savedCpuCoreSeconds := totalCpuCores * seconds
	savedMemoryGbSeconds := totalMemoryGb * seconds

	cpuRate := DefaultHourlyCpuCostUsd
	memRate := DefaultHourlyMemoryGbCostUsd

	if profile != nil {
		if profile.HourlyCpuCoreCostUsd > 0 {
			cpuRate = profile.HourlyCpuCoreCostUsd
		}
		if profile.HourlyMemoryGbCostUsd > 0 {
			memRate = profile.HourlyMemoryGbCostUsd
		}
	}

	hours := seconds / 3600.0
	cpuCost := totalCpuCores * hours * cpuRate
	memCost := totalMemoryGb * hours * memRate

	return SavingsSummary{
		Duration:             duration,
		SavedCpuCoreSeconds:  savedCpuCoreSeconds,
		SavedMemoryGbSeconds: savedMemoryGbSeconds,
		SavedUsd:             cpuCost + memCost,
	}
}
