package proxy

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/kube-zero/kube-zero/internal/finops"
	"github.com/prometheus/client_golang/prometheus"
)

// TestWakeProxy_FastPath verifies that active workloads receive requests without scaling delays.
func TestWakeProxy_FastPath(t *testing.T) {
	upstreamReceived := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamReceived = true
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok-upstream"))
	}))
	defer upstream.Close()

	u, _ := url.Parse(upstream.URL)
	scaler := NewMockScaler(2, 2) // already 2 ready replicas

	proxy, err := NewWakeProxy(Config{
		TargetURL:  u,
		Namespace:  "test-ns",
		Deployment: "test-app",
		Scaler:     scaler,
	})
	if err != nil {
		t.Fatalf("failed to create proxy: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()

	proxy.ServeHTTP(rec, req)

	if !upstreamReceived {
		t.Errorf("expected request to reach upstream directly")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}
	if scaler.WasScaleUpCalled() {
		t.Errorf("ScaleUp should not have been called on fast path")
	}
}

// TestWakeProxy_ColdStartWake verifies that sleeping workloads are scaled up and buffered payloads forwarded.
func TestWakeProxy_ColdStartWake(t *testing.T) {
	var receivedBody string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		receivedBody = string(b)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"status":"created"}`))
	}))
	defer upstream.Close()

	u, _ := url.Parse(upstream.URL)
	scaler := NewMockScaler(0, 0) // sleeping (0 desired, 0 ready)

	reg := prometheus.NewRegistry()
	metrics := finops.NewMetrics(reg)

	proxy, err := NewWakeProxy(Config{
		TargetURL:        u,
		Namespace:        "test-ns",
		Deployment:       "test-app",
		TargetReplicas:   1,
		ReadinessTimeout: 2 * time.Second,
		PollInterval:     50 * time.Millisecond,
		Scaler:           scaler,
		Metrics:          metrics,
	})
	if err != nil {
		t.Fatalf("failed to create proxy: %v", err)
	}

	// Simulate pod becoming ready after 150ms
	go func() {
		time.Sleep(150 * time.Millisecond)
		scaler.SetReady(1)
	}()

	payload := `{"order_id": 42}`
	req := httptest.NewRequest(http.MethodPost, "/api/orders", bytes.NewBufferString(payload))
	rec := httptest.NewRecorder()

	proxy.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected status 201 Created, got %d (body: %s)", rec.Code, rec.Body.String())
	}
	if !scaler.WasScaleUpCalled() {
		t.Errorf("expected ScaleUp to have been called")
	}
	if receivedBody != payload {
		t.Errorf("expected upstream to receive '%s', got '%s'", payload, receivedBody)
	}
}

// TestWakeProxy_Timeout verifies that a 504 Gateway Timeout is returned if the workload fails to start.
func TestWakeProxy_Timeout(t *testing.T) {
	u, _ := url.Parse("http://127.0.0.1:9999")
	scaler := NewMockScaler(0, 0) // will never become ready

	proxy, err := NewWakeProxy(Config{
		TargetURL:        u,
		Namespace:        "test-ns",
		Deployment:       "test-app",
		ReadinessTimeout: 100 * time.Millisecond,
		PollInterval:     20 * time.Millisecond,
		Scaler:           scaler,
	})
	if err != nil {
		t.Fatalf("failed to create proxy: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/timeout", nil)
	rec := httptest.NewRecorder()

	proxy.ServeHTTP(rec, req)

	if rec.Code != http.StatusGatewayTimeout {
		t.Errorf("expected status 504 Gateway Timeout, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "readiness timeout") {
		t.Errorf("expected error message in body, got: %s", rec.Body.String())
	}
}

// TestWakeProxy_PayloadTooLarge verifies 413 Payload Too Large when buffer limit exceeded.
func TestWakeProxy_PayloadTooLarge(t *testing.T) {
	u, _ := url.Parse("http://127.0.0.1:9999")
	scaler := NewMockScaler(0, 0)

	proxy, err := NewWakeProxy(Config{
		TargetURL:            u,
		Namespace:            "test-ns",
		Deployment:           "test-app",
		MaxBufferedBodyBytes: 10, // Small 10-byte limit
		Scaler:               scaler,
	})
	if err != nil {
		t.Fatalf("failed to create proxy: %v", err)
	}

	hugeBody := strings.Repeat("A", 100) // 100 bytes
	req := httptest.NewRequest(http.MethodPost, "/upload", strings.NewReader(hugeBody))
	rec := httptest.NewRecorder()

	proxy.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("expected status 413 Request Entity Too Large, got %d", rec.Code)
	}
}
