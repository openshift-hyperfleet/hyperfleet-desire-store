// Package server owns the HTTP listeners of the Desire Store API.
//
// There are three: the data plane on the server address, the liveness and
// readiness endpoints on the health address, per the HyperFleet
// health-endpoints standard, and /metrics on the metrics address, per the
// metrics standard. They are separate so that a saturated data plane never
// delays a probe, which would restart a busy but healthy pod.
package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"
)

// Listener timeouts. The data-plane values match the HyperFleet API defaults.
const (
	readHeaderTimeout = 10 * time.Second
	readTimeout       = 5 * time.Second
	writeTimeout      = 30 * time.Second
	idleTimeout       = 60 * time.Second

	healthReadTimeout = 5 * time.Second
)

// Options are the listener settings.
type Options struct {
	// Addr is the listen address of the data plane.
	Addr string
	// HealthAddr is the listen address of the liveness and readiness
	// endpoints.
	HealthAddr string
	// MetricsAddr is the listen address of the /metrics endpoint.
	MetricsAddr string
	// ShutdownTimeout bounds the whole graceful shutdown, drain included.
	ShutdownTimeout time.Duration
}

// Server serves the Desire Store API data plane, its health endpoints, and
// its metrics.
type Server struct {
	opts    Options
	log     *slog.Logger
	ready   atomic.Bool
	api     *http.Server
	health  *http.Server
	metrics *http.Server
}

// New builds the three listeners; api is the data-plane handler and metrics
// serves /metrics. Nothing is bound until Run is called.
func New(api, metrics http.Handler, opts Options, log *slog.Logger) *Server {
	server := &Server{opts: opts, log: log}
	server.api = &http.Server{
		Addr:              opts.Addr,
		Handler:           api,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}
	server.health = &http.Server{
		Addr:              opts.HealthAddr,
		Handler:           server.healthHandler(),
		ReadHeaderTimeout: healthReadTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}
	metricsMux := http.NewServeMux()
	metricsMux.Handle("GET /metrics", metrics)
	server.metrics = &http.Server{
		Addr:              opts.MetricsAddr,
		Handler:           metricsMux,
		ReadHeaderTimeout: healthReadTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}
	return server
}

// Run starts the three listeners and blocks until ctx is canceled or a listener
// fails. Canceling ctx starts the graceful shutdown sequence.
func (s *Server) Run(ctx context.Context) error {
	failures := make(chan error, 3)
	go s.listen("api", s.api, failures)
	go s.listen("health", s.health, failures)
	go s.listen("metrics", s.metrics, failures)

	s.ready.Store(true)
	s.log.InfoContext(ctx, "Desire Store API started",
		"server_addr", s.opts.Addr,
		"health_addr", s.opts.HealthAddr,
		"metrics_addr", s.opts.MetricsAddr,
	)

	select {
	case <-ctx.Done():
		s.log.InfoContext(ctx, "shutdown signal received")
	case err := <-failures:
		s.log.ErrorContext(ctx, "listener failed, shutting down", "error", err)
		s.ready.Store(false)
		// Shut the surviving listener down before returning, so a failure on
		// one port does not leave the process half serving.
		if shutdownErr := s.shutdown(); shutdownErr != nil {
			s.logShutdownError(ctx, "shutdown after a listener failure did not complete", shutdownErr)
		}
		return err
	}

	return s.shutdown()
}

// Shutdown implements the graceful shutdown sequence: mark not ready, drain
// the data plane, then stop the metrics listener, then stop answering probes.
// Stopping the health listener last keeps /readyz returning 503 while
// in-flight requests drain, which is what tells Kubernetes to remove the pod
// from service endpoints. Scrapes keep working during the drain. All
// listeners stop even when the drain does not finish, and the error holds the
// failure of each.
func (s *Server) Shutdown(ctx context.Context) error {
	s.ready.Store(false)
	s.log.InfoContext(ctx, "draining in-flight requests")

	apiErr := stop(ctx, "api", s.api)
	metricsErr := stop(ctx, "metrics", s.metrics)
	return errors.Join(apiErr, metricsErr, stop(ctx, "health", s.health))
}

// stop shuts one listener down gracefully. When that does not finish before
// ctx ends, it closes the connections that remain.
func stop(ctx context.Context, name string, srv *http.Server) error {
	err := srv.Shutdown(ctx)
	if err == nil {
		return nil
	}
	return fmt.Errorf("stopping the %s listener: %w", name, errors.Join(err, srv.Close()))
}

func (s *Server) shutdown() error {
	ctx, cancel := context.WithTimeout(context.Background(), s.opts.ShutdownTimeout)
	defer cancel()

	if err := s.Shutdown(ctx); err != nil {
		s.logShutdownError(ctx, "graceful shutdown did not complete", err)
		return err
	}

	s.log.InfoContext(ctx, "shutdown complete")
	return nil
}

// logShutdownError distinguishes a bounded drain timeout from other failures.
func (s *Server) logShutdownError(ctx context.Context, message string, err error) {
	level := slog.LevelError
	if errors.Is(err, context.DeadlineExceeded) {
		level = slog.LevelWarn
	}
	s.log.Log(ctx, level, message, "shutdown_timeout", s.opts.ShutdownTimeout, "error", err)
}

// healthHandler builds the probe routes. They only report process state, per
// the health-endpoints standard: no dependency is checked here.
func (s *Server) healthHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleLiveness)
	mux.HandleFunc("GET /readyz", s.handleReadiness)
	return mux
}

// listen serves one listener and reports a failure on errs. A listener that
// stops because of Shutdown is not a failure.
func (s *Server) listen(name string, srv *http.Server, errs chan<- error) {
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		errs <- errors.Join(errors.New(name+" listener"), err)
	}
}
