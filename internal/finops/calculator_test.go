package finops

import (
	"math"
	"testing"
	"time"

	"github.com/kube-zero/kube-zero/api/v1alpha1"
	"github.com/prometheus/client_golang/prometheus"
)

// TestCalculateSavings verifies correct cost and resource calculation across edge cases.
func TestCalculateSavings(t *testing.T) {
	tests := []struct {
		name          string
		cpuCores      float64
		memoryBytes   int64
		replicas      int32
		duration      time.Duration
		profile       *v1alpha1.CostProfileSpec
		expectZeroUsd bool
		minExpected   float64
		maxExpected   float64
	}{
		{
			name:          "Zero duration yields zero savings",
			cpuCores:      2.0,
			memoryBytes:   4 * BytesInGigabyte,
			replicas:      2,
			duration:      0,
			expectZeroUsd: true,
		},
		{
			name:          "Zero replicas yields zero savings",
			cpuCores:      2.0,
			memoryBytes:   4 * BytesInGigabyte,
			replicas:      0,
			duration:      10 * time.Hour,
			expectZeroUsd: true,
		},
		{
			name:          "Negative duration yields zero savings",
			cpuCores:      1.0,
			memoryBytes:   1024,
			replicas:      1,
			duration:      -5 * time.Minute,
			expectZeroUsd: true,
		},
		{
			name:        "Standard 10-hour sleep with default rates",
			cpuCores:    1.0,                 // 1 core * 2 replicas = 2 cores
			memoryBytes: 2 * BytesInGigabyte, // 2 GB * 2 replicas = 4 GB
			replicas:    2,
			duration:    10 * time.Hour,
			profile:     nil,
			// Expected CPU: 2 cores * 10 hours * 0.0416 = 0.832
			// Expected RAM: 4 GB * 10 hours * 0.0052 = 0.208
			// Total = 1.04 USD
			minExpected: 1.03,
			maxExpected: 1.05,
		},
		{
			name:        "Custom cost profile overrides",
			cpuCores:    4.0,
			memoryBytes: 8 * BytesInGigabyte,
			replicas:    1,
			duration:    1 * time.Hour,
			profile: &v1alpha1.CostProfileSpec{
				HourlyCpuCoreCostUsd:  0.10,
				HourlyMemoryGbCostUsd: 0.02,
			},
			// Expected: 4 cores * 1 hr * 0.10 = 0.40
			// RAM: 8 GB * 1 hr * 0.02 = 0.16
			// Total = 0.56 USD
			minExpected: 0.559,
			maxExpected: 0.561,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res := CalculateSavings(tc.cpuCores, tc.memoryBytes, tc.replicas, tc.duration, tc.profile)

			if tc.expectZeroUsd {
				if res.SavedUsd != 0 {
					t.Fatalf("expected 0 USD savings, got %f", res.SavedUsd)
				}
				return
			}

			if res.SavedUsd < tc.minExpected || res.SavedUsd > tc.maxExpected {
				t.Fatalf("expected savings between [%f, %f], got %f", tc.minExpected, tc.maxExpected, res.SavedUsd)
			}

			if math.IsNaN(res.SavedUsd) || math.IsInf(res.SavedUsd, 0) {
				t.Fatalf("savings returned NaN or Inf")
			}
		})
	}
}

// TestMetricsRegistration verifies that all collectors register without panic.
func TestMetricsRegistration(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := NewMetrics(reg)

	if m.SleepingDeployments == nil || m.WakeEventsTotal == nil || m.WakeDurationSeconds == nil || m.CostSavingsUsdTotal == nil {
		t.Fatalf("one or more metrics collectors were not initialized")
	}

	m.SleepingDeployments.WithLabelValues("dev", "order-service").Set(1)
	m.WakeEventsTotal.WithLabelValues("dev", "order-service", "success").Inc()
	m.WakeDurationSeconds.WithLabelValues("dev", "order-service").Observe(4.2)
	m.CostSavingsUsdTotal.WithLabelValues("dev", "order-service").Add(12.50)
}
