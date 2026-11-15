// Command controller runs the KubeZero operator reconciler daemon.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/kube-zero/kube-zero/api/v1alpha1"
	"github.com/kube-zero/kube-zero/internal/controller"
	"github.com/kube-zero/kube-zero/internal/finops"
	"github.com/kube-zero/kube-zero/internal/k8s"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
)

// getEnv retrieves an environment variable or returns defaultVal if unset.
func getEnv(key, defaultVal string) string {
	if val, ok := os.LookupEnv(key); ok && val != "" {
		return val
	}
	return defaultVal
}

// ptrInt32 returns a pointer to an int32 value.
func ptrInt32(n int32) *int32 {
	return &n
}

// reconcileAllPolicies lists and reconciles all ZeroPolicy CRDs against target workloads.
//
// Parameters:
//   - ctx: Context for network cancellation.
//   - dynClient: Kubernetes dynamic client for querying and updating ZeroPolicy CRDs.
//   - reconciler: ZeroPolicyReconciler instance executing schedule and scaling decisions.
//   - namespace: Target namespace, or empty string to reconcile across all namespaces.
//   - now: Current evaluation timestamp.
//   - logger: Structured logger.
func reconcileAllPolicies(
	ctx context.Context,
	dynClient dynamic.Interface,
	reconciler *controller.ZeroPolicyReconciler,
	namespace string,
	now time.Time,
	logger *slog.Logger,
) {
	if dynClient == nil || reconciler == nil {
		return
	}

	policies, err := k8s.ListZeroPolicies(ctx, dynClient, namespace)
	if err != nil {
		logger.Error("failed to query ZeroPolicy resources", "namespace", namespace, "err", err)
		return
	}

	logger.Debug("evaluating ZeroPolicies", "count", len(policies), "namespace", namespace)
	for i := range policies {
		policy := &policies[i]
		newStatus, err := reconciler.Reconcile(ctx, policy, now)
		if err != nil {
			logger.Error("reconciliation failed for policy",
				"namespace", policy.Namespace,
				"name", policy.Name,
				"err", err,
			)
			continue
		}

		if err := k8s.UpdateZeroPolicyStatus(ctx, dynClient, policy.Namespace, policy.Name, newStatus); err != nil {
			logger.Error("failed to update ZeroPolicy status",
				"namespace", policy.Namespace,
				"name", policy.Name,
				"err", err,
			)
		} else {
			logger.Debug("reconciled ZeroPolicy successfully",
				"namespace", policy.Namespace,
				"name", policy.Name,
				"phase", newStatus.Phase,
			)
		}
	}
}

func main() {
	var (
		metricsPortFlag = flag.String("metrics-port", getEnv("METRICS_PORT", "9091"), "Port for metrics and health probes")
		syncInterval    = flag.Duration("sync-interval", 30*time.Second, "Reconciliation polling cadence")
		namespaceFlag   = flag.String("namespace", getEnv("NAMESPACE", ""), "Namespace to watch (empty string watches all namespaces)")
		kubeconfigFlag  = flag.String("kubeconfig", getEnv("KUBECONFIG", ""), "Path to kubeconfig file")
		mockModeFlag    = flag.Bool("mock", getEnv("MOCK_MODE", "false") == "true", "Run in mock mode without live cluster")
	)
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	var k8sClient kubernetes.Interface
	var dynClient dynamic.Interface

	if *mockModeFlag {
		logger.Info("running controller in mock mode with fake Kubernetes client")
		mockDeploy := &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "sample-app",
				Namespace: "default",
			},
			Spec: appsv1.DeploymentSpec{
				Replicas: ptrInt32(3),
				Template: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{
								Name: "app",
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
		k8sClient = fake.NewSimpleClientset(mockDeploy)

		samplePolicy := &v1alpha1.ZeroPolicy{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "finops.kubezero.io/v1alpha1",
				Kind:       "ZeroPolicy",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      "sample-app-policy",
				Namespace: "default",
			},
			Spec: v1alpha1.ZeroPolicySpec{
				TargetRef: v1alpha1.TargetReference{
					Kind: "Deployment",
					Name: "sample-app",
				},
				Inactivity: &v1alpha1.InactivitySpec{
					Enabled:     true,
					IdleTimeout: "10m",
				},
			},
		}
		u, _ := k8s.ZeroPolicyToUnstructured(samplePolicy)
		scheme := runtime.NewScheme()
		gvrToListKind := map[schema.GroupVersionResource]string{
			k8s.ZeroPolicyGVR: "ZeroPolicyList",
		}
		dynClient = dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, gvrToListKind, u)
	} else {
		client, err := k8s.NewClientset(*kubeconfigFlag)
		if err != nil {
			logger.Warn("could not connect to Kubernetes API; falling back to mock client for offline run", "err", err)
			k8sClient = fake.NewSimpleClientset()
		} else {
			logger.Info("connected to Kubernetes API server")
			k8sClient = client
		}

		dynamicClient, err := k8s.NewDynamicClient(*kubeconfigFlag)
		if err != nil {
			logger.Warn("could not initialize dynamic client; falling back to fake dynamic client", "err", err)
			scheme := runtime.NewScheme()
			dynClient = dynamicfake.NewSimpleDynamicClient(scheme)
		} else {
			dynClient = dynamicClient
		}
	}

	reg := prometheus.DefaultRegisterer
	metrics := finops.NewMetrics(reg)
	reconciler := controller.NewZeroPolicyReconciler(k8sClient, metrics, logger)

	// Metrics & Health server
	metricsMux := http.NewServeMux()
	metricsMux.Handle("/metrics", promhttp.Handler())
	metricsMux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	metricsMux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	metricsServer := &http.Server{
		Addr:    fmt.Sprintf(":%s", *metricsPortFlag),
		Handler: metricsMux,
	}

	go func() {
		logger.Info("starting controller metrics server", "addr", metricsServer.Addr)
		if err := metricsServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("metrics server error", "err", err)
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Initial reconciliation on startup
	logger.Info("running initial reconciliation pass")
	reconcileAllPolicies(ctx, dynClient, reconciler, *namespaceFlag, time.Now(), logger)

	// Periodic reconciliation ticker
	go func() {
		ticker := time.NewTicker(*syncInterval)
		defer ticker.Stop()

		logger.Info("KubeZero reconciler loop started", "interval", syncInterval.String())
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				logger.Debug("running scheduled reconcile tick", "time", now.Format(time.RFC3339))
				reconcileAllPolicies(ctx, dynClient, reconciler, *namespaceFlag, now, logger)
			}
		}
	}()

	// Graceful shutdown on SIGINT/SIGTERM
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan

	logger.Info("shutdown signal received; stopping controller")
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	_ = metricsServer.Shutdown(shutdownCtx)
	logger.Info("KubeZero Controller stopped cleanly")
}
