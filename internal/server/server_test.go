package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/openshift-hyperfleet/hyperfleet-desire-store/internal/config"
)

const testAddr = "127.0.0.1:0"

func newTestServer(t *testing.T) *Server {
	t.Helper()
	return New(config.Config{
		ServerAddr:      testAddr,
		HealthAddr:      testAddr,
		ShutdownTimeout: 5 * time.Second,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func get(t *testing.T, handler http.Handler, path string) (*http.Response, probeResponse) {
	t.Helper()

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
	response := recorder.Result()

	var body probeResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decoding %s response: %v", path, err)
	}
	t.Cleanup(func() { _ = response.Body.Close() })

	return response, body
}

func TestLivenessAlwaysReportsOK(t *testing.T) {
	server := newTestServer(t)

	response, body := get(t, server.healthHandler(), "/healthz")

	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	if body.Status != statusOK {
		t.Fatalf("status body = %q, want %q", body.Status, statusOK)
	}
	if body.Checks != nil {
		t.Fatalf("liveness must not report checks, got %v", body.Checks)
	}
}

func TestReadinessIsUnavailableUntilReady(t *testing.T) {
	server := newTestServer(t)

	response, body := get(t, server.healthHandler(), "/readyz")

	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusServiceUnavailable)
	}
	if body.Status != statusError {
		t.Fatalf("status body = %q, want %q", body.Status, statusError)
	}
	if body.Checks[checkConfig] != statusOK {
		t.Fatalf("config check = %q, want %q", body.Checks[checkConfig], statusOK)
	}
}

func TestReadinessReportsOKWhenReady(t *testing.T) {
	server := newTestServer(t)
	server.ready.Store(true)

	response, body := get(t, server.healthHandler(), "/readyz")

	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	if body.Status != statusOK {
		t.Fatalf("status body = %q, want %q", body.Status, statusOK)
	}
	if body.Message != "" {
		t.Fatalf("ready response must not carry a message, got %q", body.Message)
	}
}

func TestHealthEndpointsRejectOtherMethods(t *testing.T) {
	server := newTestServer(t)

	recorder := httptest.NewRecorder()
	server.healthHandler().ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/healthz", nil))

	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusMethodNotAllowed)
	}
}

func TestAPIServerHasNoRoutesYet(t *testing.T) {
	server := newTestServer(t)

	recorder := httptest.NewRecorder()
	server.apiHandler().ServeHTTP(recorder,
		httptest.NewRequest(http.MethodGet, "/api/hyperfleet/desire-store/v1/partitions/mc-01/desires", nil))

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d until the service implementation adds routes",
			recorder.Code, http.StatusNotFound)
	}
}

func TestRunReturnsAfterContextCancellation(t *testing.T) {
	server := newTestServer(t)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Run(ctx) }()

	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned %v, want nil after a graceful shutdown", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return within the shutdown timeout")
	}

	if server.ready.Load() {
		t.Fatal("server must not be ready after shutdown")
	}
}

func TestRunFailsWhenTheHealthAddressIsInUse(t *testing.T) {
	occupied := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(occupied.Close)

	server := New(config.Config{
		ServerAddr:      testAddr,
		HealthAddr:      occupied.Listener.Addr().String(),
		ShutdownTimeout: 5 * time.Second,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := server.Run(ctx); err == nil {
		t.Fatal("Run must fail when a listener cannot bind")
	}
}
