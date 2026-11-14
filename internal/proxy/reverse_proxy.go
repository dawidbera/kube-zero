package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"

	"github.com/kube-zero/kube-zero/internal/finops"
)

// Config holds runtime configuration options for the Wake-on-Traffic reverse proxy.
type Config struct {
	// TargetURL is the destination URL of the upstream service (e.g. http://10.96.0.12:8080).
	TargetURL *url.URL

	// Namespace of the managed deployment.
	Namespace string

	// Deployment name of the managed workload.
	Deployment string

	// TargetReplicas count to scale up to upon receiving traffic (default 1).
	TargetReplicas int32

	// ReadinessTimeout is the maximum duration to wait for at least one ready pod (default 30s).
	ReadinessTimeout time.Duration

	// PollInterval is the polling cadence to check pod readiness (default 250ms).
	PollInterval time.Duration

	// MaxBufferedBodyBytes is the payload memory limit during cold-start buffering (default 10MB).
	MaxBufferedBodyBytes int64

	// Scaler interface to interact with Kubernetes deployment state.
	Scaler Scaler

	// Metrics telemetry registry (optional).
	Metrics *finops.Metrics

	// Logger for structured output.
	Logger *slog.Logger
}

// WakeProxy intercepts incoming HTTP traffic, waking sleeping workloads before reverse-proxying.
type WakeProxy struct {
	cfg          Config
	reverseProxy *httputil.ReverseProxy
	logger       *slog.Logger
}

// NewWakeProxy constructs and validates a WakeProxy instance.
func NewWakeProxy(cfg Config) (*WakeProxy, error) {
	if cfg.TargetURL == nil {
		return nil, errors.New("TargetURL must not be nil")
	}
	if cfg.Namespace == "" || cfg.Deployment == "" {
		return nil, errors.New("Namespace and Deployment must be specified")
	}
	if cfg.Scaler == nil {
		return nil, errors.New("Scaler must not be nil")
	}

	if cfg.TargetReplicas <= 0 {
		cfg.TargetReplicas = 1
	}
	if cfg.ReadinessTimeout <= 0 {
		cfg.ReadinessTimeout = 30 * time.Second
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 250 * time.Millisecond
	}
	if cfg.MaxBufferedBodyBytes <= 0 {
		cfg.MaxBufferedBodyBytes = 10 * 1024 * 1024 // 10MB
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}

	rp := httputil.NewSingleHostReverseProxy(cfg.TargetURL)

	return &WakeProxy{
		cfg:          cfg,
		reverseProxy: rp,
		logger:       cfg.Logger,
	}, nil
}

// ServeHTTP handles incoming requests. If the target workload is scaled to zero, it buffers the payload,
// triggers a scale-up, waits for readiness, and forwards the request transparently once ready.
func (p *WakeProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	desired, ready, err := p.cfg.Scaler.GetDeploymentState(ctx, p.cfg.Namespace, p.cfg.Deployment)
	if err != nil {
		p.logger.Error("failed to get deployment state", "namespace", p.cfg.Namespace, "deployment", p.cfg.Deployment, "err", err)
		p.writeJSONError(w, http.StatusBadGateway, "failed to query workload state", err.Error())
		return
	}

	// Fast path: Workload is already active and ready
	if ready > 0 {
		p.reverseProxy.ServeHTTP(w, r)
		return
	}

	// Cold path: Workload is sleeping or has no ready replicas
	p.logger.Info("cold start triggered for sleeping workload",
		"namespace", p.cfg.Namespace,
		"deployment", p.cfg.Deployment,
		"desired", desired,
		"ready", ready,
		"path", r.URL.Path,
	)

	// Buffer body to prevent loss during scale-up wait
	if _, err := BufferedRequestBody(r, p.cfg.MaxBufferedBodyBytes); err != nil {
		if errors.Is(err, ErrPayloadTooLarge) {
			p.writeJSONError(w, http.StatusRequestEntityTooLarge, "payload too large for cold start buffer", err.Error())
			return
		}
		p.writeJSONError(w, http.StatusBadRequest, "failed to buffer request body", err.Error())
		return
	}

	wakeStart := time.Now()

	// Issue scale-up command
	targetReplicas := p.cfg.TargetReplicas
	if desired > 0 {
		targetReplicas = desired
	}

	if err := p.cfg.Scaler.ScaleUp(ctx, p.cfg.Namespace, p.cfg.Deployment, targetReplicas); err != nil {
		p.logger.Error("failed to scale up workload", "namespace", p.cfg.Namespace, "deployment", p.cfg.Deployment, "err", err)
		if p.cfg.Metrics != nil {
			p.cfg.Metrics.WakeEventsTotal.WithLabelValues(p.cfg.Namespace, p.cfg.Deployment, "scale_error").Inc()
		}
		p.writeJSONError(w, http.StatusBadGateway, "failed to trigger scale-up", err.Error())
		return
	}

	// Wait for pod readiness
	if err := p.waitForReady(ctx); err != nil {
		p.logger.Warn("workload failed to become ready in time",
			"namespace", p.cfg.Namespace,
			"deployment", p.cfg.Deployment,
			"duration", time.Since(wakeStart),
			"err", err,
		)
		if p.cfg.Metrics != nil {
			p.cfg.Metrics.WakeEventsTotal.WithLabelValues(p.cfg.Namespace, p.cfg.Deployment, "timeout").Inc()
		}
		p.writeJSONError(w, http.StatusGatewayTimeout, "workload failed to start within timeout", err.Error())
		return
	}

	wakeDuration := time.Since(wakeStart)
	p.logger.Info("workload successfully woken up",
		"namespace", p.cfg.Namespace,
		"deployment", p.cfg.Deployment,
		"wakeDurationMs", wakeDuration.Milliseconds(),
	)

	if p.cfg.Metrics != nil {
		p.cfg.Metrics.WakeEventsTotal.WithLabelValues(p.cfg.Namespace, p.cfg.Deployment, "success").Inc()
		p.cfg.Metrics.WakeDurationSeconds.WithLabelValues(p.cfg.Namespace, p.cfg.Deployment).Observe(wakeDuration.Seconds())
	}

	// Forward the buffered request to the freshly revived workload
	p.reverseProxy.ServeHTTP(w, r)
}

// waitForReady polls the Scaler until at least one replica is ready or timeout occurs.
func (p *WakeProxy) waitForReady(ctx context.Context) error {
	deadline := time.Now().Add(p.cfg.ReadinessTimeout)
	ticker := time.NewTicker(p.cfg.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case now := <-ticker.C:
			if now.After(deadline) {
				return fmt.Errorf("readiness timeout (%s) exceeded", p.cfg.ReadinessTimeout)
			}

			_, ready, err := p.cfg.Scaler.GetDeploymentState(ctx, p.cfg.Namespace, p.cfg.Deployment)
			if err != nil {
				p.logger.Debug("polling error during wake-up wait", "err", err)
				continue
			}

			if ready > 0 {
				return nil
			}
		}
	}
}

// writeJSONError renders a consistent JSON error message.
func (p *WakeProxy) writeJSONError(w http.ResponseWriter, statusCode int, message, detail string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error":      message,
		"detail":     detail,
		"namespace":  p.cfg.Namespace,
		"deployment": p.cfg.Deployment,
		"statusCode": statusCode,
	})
}
