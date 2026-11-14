package finops

import (
	"github.com/prometheus/client_golang/prometheus"
)

// Metrics encapsulates Prometheus collectors for KubeZero operations and FinOps ROI.
type Metrics struct {
	// SleepingDeployments tracks currently sleeping deployments by namespace and name.
	SleepingDeployments *prometheus.GaugeVec

	// WakeEventsTotal counts total wake attempts, partitioned by target and status (success/timeout/error).
	WakeEventsTotal *prometheus.CounterVec

	// WakeDurationSeconds observes the time taken from receiving the HTTP request to the pod becoming ready.
	WakeDurationSeconds *prometheus.HistogramVec

	// CostSavingsUsdTotal accumulates estimated financial savings in USD per workload.
	CostSavingsUsdTotal *prometheus.CounterVec
}

// NewMetrics creates and registers Prometheus metrics collectors with the provided registerer.
// If registerer is nil, prometheus.DefaultRegisterer is used.
func NewMetrics(reg prometheus.Registerer) *Metrics {
	if reg == nil {
		reg = prometheus.DefaultRegisterer
	}

	m := &Metrics{
		SleepingDeployments: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "kubezero",
				Name:      "sleeping_deployments",
				Help:      "Current number of deployments actively scaled down to zero replicas.",
			},
			[]string{"namespace", "deployment"},
		),
		WakeEventsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "kubezero",
				Name:      "wake_events_total",
				Help:      "Total count of Wake-on-Traffic trigger attempts.",
			},
			[]string{"namespace", "deployment", "status"},
		),
		WakeDurationSeconds: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: "kubezero",
				Name:      "wake_duration_seconds",
				Help:      "Duration in seconds from HTTP trigger to target pod readiness.",
				Buckets:   []float64{1.0, 2.5, 5.0, 10.0, 15.0, 30.0, 45.0, 60.0},
			},
			[]string{"namespace", "deployment"},
		),
		CostSavingsUsdTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "kubezero",
				Name:      "cost_savings_usd_total",
				Help:      "Estimated accumulated financial savings in US Dollars.",
			},
			[]string{"namespace", "deployment"},
		),
	}

	reg.MustRegister(m.SleepingDeployments)
	reg.MustRegister(m.WakeEventsTotal)
	reg.MustRegister(m.WakeDurationSeconds)
	reg.MustRegister(m.CostSavingsUsdTotal)

	return m
}
