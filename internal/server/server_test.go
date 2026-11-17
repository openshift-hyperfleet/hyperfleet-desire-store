package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

const testAddr = "127.0.0.1:0"

func newTestServer(t *testing.T) *Server {
	t.Helper()
	return New(http.NewServeMux(), Options{
		Addr:            testAddr,
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

// TestReadinessIsUnavailableDuringTheDrain runs both listeners, holds one
// data-plane request open, and cancels Run. /readyz must answer 503 while the
// request drains, and the request must still complete.
func TestReadinessIsUnavailableDuringTheDrain(t *testing.T) {
	apiAddr, healthAddr := freeAddr(t), freeAddr(t)
	entered, release := make(chan struct{}), make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("GET /ping", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	mux.HandleFunc("GET /slow", func(w http.ResponseWriter, _ *http.Request) {
		close(entered)
		<-release
		w.WriteHeader(http.StatusNoContent)
	})
	server := New(mux, Options{Addr: apiAddr, HealthAddr: healthAddr, ShutdownTimeout: 10 * time.Second},
		slog.New(slog.NewTextHandler(io.Discard, nil)))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- server.Run(ctx) }()

	readyz := "http://" + healthAddr + "/readyz"
	waitForStatus(t, readyz, http.StatusOK)
	waitForStatus(t, "http://"+apiAddr+"/ping", http.StatusNoContent)

	inflight := make(chan int, 1)
	go func() { inflight <- statusOf("http://" + apiAddr + "/slow") }()
	select {
	case <-entered:
	case code := <-inflight:
		t.Fatalf("the slow request ended with status %d before it reached the handler", code)
	case <-time.After(5 * time.Second):
		t.Fatal("the slow request did not reach the handler within 5s")
	}
	cancel()

	waitForStatus(t, readyz, http.StatusServiceUnavailable)
	close(release)
	if code := <-inflight; code != http.StatusNoContent {
		t.Fatalf("in-flight request status = %d, want %d after the drain", code, http.StatusNoContent)
	}
	if err := <-done; err != nil {
		t.Fatalf("Run returned %v, want nil after a graceful shutdown", err)
	}
}

// freeAddr returns a loopback address with a port that was free a moment ago.
func freeAddr(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("finding a free port: %v", err)
	}
	addr := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("releasing the free port: %v", err)
	}
	return addr
}

// statusOf returns the status of a GET, or 0 when the request fails.
func statusOf(url string) int {
	response, err := http.Get(url) //nolint:noctx // a test request bounded by the server's own shutdown
	if err != nil {
		return 0
	}
	_ = response.Body.Close()
	return response.StatusCode
}

// waitForStatus polls url until it answers want, for at most five seconds.
func waitForStatus(t *testing.T, url string, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for statusOf(url) != want {
		if time.Now().After(deadline) {
			t.Fatalf("%s did not answer %d within 5s", url, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestRunFailsWhenTheHealthAddressIsInUse(t *testing.T) {
	occupied := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(occupied.Close)

	server := New(http.NewServeMux(), Options{
		Addr:            testAddr,
		HealthAddr:      occupied.Listener.Addr().String(),
		ShutdownTimeout: 5 * time.Second,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := server.Run(ctx); err == nil {
		t.Fatal("Run must fail when a listener cannot bind")
	}
}
