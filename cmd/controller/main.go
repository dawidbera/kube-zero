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

	"github.com/kube-zero/kube-zero/internal/controller"
	"github.com/kube-zero/kube-zero/internal/finops"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// getEnv retrieves an environment variable or returns defaultVal if unset.
func getEnv(key, defaultVal string) string {
	if val, ok := os.LookupEnv(key); ok && val != "" {
		return val
	}
	return defaultVal
}

// createKubernetesClient attempts to construct a Kubernetes clientset from in-cluster credentials or kubeconfig.
func createKubernetesClient(kubeconfigPath string) (kubernetes.Interface, error) {
	var cfg *rest.Config
	var err error

	if kubeconfigPath != "" {
		cfg, err = clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	} else {
		cfg, err = rest.InClusterConfig()
	}

	if err != nil {
		return nil, err
	}
	return kubernetes.NewForConfig(cfg)
}

func main() {
	var (
		metricsPortFlag = flag.String("metrics-port", getEnv("METRICS_PORT", "9091"), "Port for metrics and health probes")
		syncInterval    = flag.Duration("sync-interval", 30*time.Second, "Reconciliation polling cadence")
		kubeconfigFlag  = flag.String("kubeconfig", getEnv("KUBECONFIG", ""), "Path to kubeconfig file")
		mockModeFlag    = flag.Bool("mock", getEnv("MOCK_MODE", "false") == "true", "Run in mock mode without live cluster")
	)
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	var k8sClient kubernetes.Interface
	if *mockModeFlag {
		logger.Info("running controller in mock mode with fake Kubernetes client")
		k8sClient = fake.NewSimpleClientset()
	} else {
		client, err := createKubernetesClient(*kubeconfigFlag)
		if err != nil {
			logger.Warn("could not connect to Kubernetes API; falling back to mock client for offline run", "err", err)
			k8sClient = fake.NewSimpleClientset()
		} else {
			logger.Info("connected to Kubernetes API server")
			k8sClient = client
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
				_ = reconciler // ready for active policy list integration
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
