// Command wake-proxy runs the KubeZero Wake-on-Traffic reverse proxy daemon.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/kube-zero/kube-zero/internal/finops"
	"github.com/kube-zero/kube-zero/internal/k8s"
	"github.com/kube-zero/kube-zero/internal/proxy"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// getEnv retrieves an environment variable or returns the default value if unset.
func getEnv(key, defaultVal string) string {
	if val, ok := os.LookupEnv(key); ok && val != "" {
		return val
	}
	return defaultVal
}

// getEnvInt64 retrieves an integer environment variable or returns defaultVal on parse failure.
func getEnvInt64(key string, defaultVal int64) int64 {
	if val, ok := os.LookupEnv(key); ok && val != "" {
		if parsed, err := strconv.ParseInt(val, 10, 64); err == nil {
			return parsed
		}
	}
	return defaultVal
}

func main() {
	var (
		portFlag             = flag.String("port", getEnv("PORT", "8080"), "Port for proxy to listen on")
		metricsPortFlag      = flag.String("metrics-port", getEnv("METRICS_PORT", "9090"), "Port for metrics and health probes")
		targetURLFlag        = flag.String("target-url", getEnv("TARGET_URL", "http://127.0.0.1:3000"), "Upstream service target URL")
		namespaceFlag        = flag.String("namespace", getEnv("NAMESPACE", "default"), "Target workload namespace")
		deploymentFlag       = flag.String("deployment", getEnv("DEPLOYMENT", "sample-app"), "Target workload deployment name")
		readinessTimeoutFlag = flag.Duration("readiness-timeout", 45*time.Second, "Max duration to wait for pod readiness")
		kubeconfigFlag       = flag.String("kubeconfig", getEnv("KUBECONFIG", ""), "Path to kubeconfig file")
		mockModeFlag         = flag.Bool("mock", getEnv("MOCK_MODE", "false") == "true", "Run in mock mode without live K8s")
	)
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	targetParsed, err := url.Parse(*targetURLFlag)
	if err != nil {
		logger.Error("invalid target URL", "url", *targetURLFlag, "err", err)
		os.Exit(1)
	}

	var scaler proxy.Scaler
	if *mockModeFlag {
		logger.Info("running in mock mode with in-memory scaler", "namespace", *namespaceFlag, "deployment", *deploymentFlag)
		scaler = proxy.NewMockScaler(1, 1)
	} else {
		k8sClient, err := k8s.NewClientset(*kubeconfigFlag)
		if err != nil {
			logger.Warn("could not connect to Kubernetes API; falling back to MockScaler for local execution", "err", err)
			scaler = proxy.NewMockScaler(1, 1)
		} else {
			logger.Info("connected to Kubernetes API", "namespace", *namespaceFlag, "deployment", *deploymentFlag)
			scaler = proxy.NewKubernetesScaler(k8sClient)
		}
	}

	reg := prometheus.DefaultRegisterer
	metrics := finops.NewMetrics(reg)

	wakeProxy, err := proxy.NewWakeProxy(proxy.Config{
		TargetURL:            targetParsed,
		Namespace:            *namespaceFlag,
		Deployment:           *deploymentFlag,
		TargetReplicas:       1,
		ReadinessTimeout:     *readinessTimeoutFlag,
		PollInterval:         250 * time.Millisecond,
		MaxBufferedBodyBytes: getEnvInt64("MAX_BUFFERED_BODY_BYTES", 10*1024*1024),
		Scaler:               scaler,
		Metrics:              metrics,
		Logger:               logger,
	})
	if err != nil {
		logger.Error("failed to initialize WakeProxy", "err", err)
		os.Exit(1)
	}

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

	// Main Proxy Server
	proxyServer := &http.Server{
		Addr:    fmt.Sprintf(":%s", *portFlag),
		Handler: wakeProxy,
	}

	go func() {
		logger.Info("starting metrics server", "addr", metricsServer.Addr)
		if err := metricsServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("metrics server error", "err", err)
		}
	}()

	go func() {
		logger.Info("starting wake-on-traffic proxy server", "addr", proxyServer.Addr, "target", targetParsed.String())
		if err := proxyServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("proxy server error", "err", err)
		}
	}()

	// Graceful shutdown on SIGINT/SIGTERM
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan

	logger.Info("shutdown signal received; draining active connections")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_ = proxyServer.Shutdown(shutdownCtx)
	_ = metricsServer.Shutdown(shutdownCtx)
	logger.Info("KubeZero Wake-Proxy stopped cleanly")
}
