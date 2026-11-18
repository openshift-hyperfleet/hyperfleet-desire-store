package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
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
	entered, released := make(chan struct{}), make(chan struct{})
	release := sync.OnceFunc(func() { close(released) })
	t.Cleanup(release)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /ping", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	mux.HandleFunc("GET /slow", func(w http.ResponseWriter, _ *http.Request) {
		close(entered)
		<-released
		w.WriteHeader(http.StatusNoContent)
	})
	server := New(mux, Options{Addr: apiAddr, HealthAddr: healthAddr, ShutdownTimeout: 10 * time.Second},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	// /readyz turns 503 before the API drain starts, so the test waits for the
	// drain itself before it lets the slow request finish.
	draining := make(chan struct{})
	server.api.RegisterOnShutdown(func() { close(draining) })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- server.Run(ctx) }()

	readyz := "http://" + healthAddr + "/readyz"
	waitForStatus(t, readyz, http.StatusOK)
	waitForStatus(t, "http://"+apiAddr+"/ping", http.StatusNoContent)

	inflight := make(chan *http.Response, 1)
	go func() { inflight <- fetch(t.Context(), "http://"+apiAddr+"/slow") }()
	select {
	case <-entered:
	case response := <-inflight:
		t.Fatalf("the slow request ended with %v before it reached the handler", response)
	case <-time.After(5 * time.Second):
		t.Fatal("the slow request did not reach the handler within 5s")
	}
	cancel()

	waitForStatus(t, readyz, http.StatusServiceUnavailable)
	select {
	case <-draining:
	case <-time.After(5 * time.Second):
		t.Fatal("the API listener did not start draining within 5s")
	}
	release()
	response := <-inflight
	if response == nil || response.StatusCode != http.StatusNoContent {
		t.Fatalf("in-flight request = %v, want status %d after the drain", response, http.StatusNoContent)
	}
	// The response is written during the drain, so the client must not reuse
	// the connection.
	if !response.Close {
		t.Fatal("a response written during the drain must carry Connection: close")
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
func statusOf(ctx context.Context, url string) int {
	response := fetch(ctx, url)
	if response == nil {
		return 0
	}
	return response.StatusCode
}

// fetch sends a GET and returns the response with its body closed, or nil
// when the request fails.
func fetch(ctx context.Context, url string) *http.Response {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil
	}
	_ = response.Body.Close()
	return response
}

// waitForStatus polls url until it answers want, for at most five seconds. A
// request that stalls ends at the deadline too.
func waitForStatus(t *testing.T, url string, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	ctx, cancel := context.WithDeadline(t.Context(), deadline)
	defer cancel()
	for statusOf(ctx, url) != want {
		if time.Now().After(deadline) {
			t.Fatalf("%s did not answer %d within 5s", url, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestShutdownTimeoutStopsBothListeners holds a data-plane request and an
// unfinished health request open past the shutdown timeout. Run must still
// stop both listeners and close both connections, and its error must report
// the failure of each listener.
func TestShutdownTimeoutStopsBothListeners(t *testing.T) {
	apiAddr, healthAddr := freeAddr(t), freeAddr(t)
	entered, release := make(chan struct{}), make(chan struct{})
	t.Cleanup(func() { close(release) })
	mux := http.NewServeMux()
	mux.HandleFunc("GET /slow", func(w http.ResponseWriter, _ *http.Request) {
		close(entered)
		<-release
		w.WriteHeader(http.StatusNoContent)
	})
	server := New(mux, Options{Addr: apiAddr, HealthAddr: healthAddr, ShutdownTimeout: 200 * time.Millisecond},
		slog.New(slog.NewTextHandler(io.Discard, nil)))

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- server.Run(ctx) }()
	waitForStatus(t, "http://"+healthAddr+"/readyz", http.StatusOK)

	inflight := make(chan int, 1)
	go func() { inflight <- statusOf(t.Context(), "http://"+apiAddr+"/slow") }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the slow request did not reach the handler within 5s")
	}
	// A request whose header never ends keeps the health listener busy.
	probe, err := net.Dial("tcp", healthAddr)
	if err != nil {
		t.Fatalf("connecting to the health listener: %v", err)
	}
	t.Cleanup(func() { _ = probe.Close() })
	if _, err := probe.Write([]byte("GET /readyz HTTP/1.1\r\n")); err != nil {
		t.Fatalf("starting a health request: %v", err)
	}

	cancel()
	var runErr error
	select {
	case runErr = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return within 5s of a 200ms shutdown timeout")
	}
	if !errors.Is(runErr, context.DeadlineExceeded) {
		t.Fatalf("Run returned %v, want the drain deadline", runErr)
	}
	for _, listener := range []string{"api", "health"} {
		if !strings.Contains(runErr.Error(), "stopping the "+listener+" listener") {
			t.Fatalf("Run returned %v, want the failure of the %s listener", runErr, listener)
		}
	}

	if code := <-inflight; code != 0 {
		t.Fatalf("in-flight request status = %d, want its connection closed", code)
	}
	if err := probe.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("setting a read deadline: %v", err)
	}
	if _, err := probe.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Fatalf("reading the unfinished health request = %v, want its connection closed", err)
	}
	for _, addr := range []string{apiAddr, healthAddr} {
		if conn, err := net.DialTimeout("tcp", addr, time.Second); err == nil {
			_ = conn.Close()
			t.Fatalf("%s still accepts connections after Run returned", addr)
		}
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
