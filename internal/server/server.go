// Package server owns the HTTP listeners of the Desire Store API.
//
// There are two of them, per the HyperFleet health-endpoints standard: the
// data plane on the server address, and the liveness and readiness endpoints
// on the health address. They are separate so that a saturated data plane
// never delays a probe, which would restart a busy but healthy pod.
package server

import (
	"context"
	"errors"
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
	// ShutdownTimeout bounds the whole graceful shutdown, drain included.
	ShutdownTimeout time.Duration
}

// Server serves the Desire Store API data plane and its health endpoints.
type Server struct {
	opts   Options
	log    *slog.Logger
	ready  atomic.Bool
	api    *http.Server
	health *http.Server
}

// New builds both listeners; api is the data-plane handler. Nothing is bound
// until Run is called.
func New(api http.Handler, opts Options, log *slog.Logger) *Server {
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
		IdleTimeout:       idleTimeout,
	}
	return server
}

// Run starts both listeners and blocks until ctx is canceled or a listener
// fails. Canceling ctx starts the graceful shutdown sequence.
func (s *Server) Run(ctx context.Context) error {
	failures := make(chan error, 2)
	go s.listen("api", s.api, failures)
	go s.listen("health", s.health, failures)

	s.ready.Store(true)
	s.log.InfoContext(ctx, "Desire Store API started",
		"server_addr", s.opts.Addr,
		"health_addr", s.opts.HealthAddr,
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
// the data plane, then stop answering probes. Stopping the health listener
// last keeps /readyz returning 503 while in-flight requests drain, which is
// what tells Kubernetes to remove the pod from service endpoints.
func (s *Server) Shutdown(ctx context.Context) error {
	s.ready.Store(false)
	s.log.InfoContext(ctx, "draining in-flight requests")

	if err := s.api.Shutdown(ctx); err != nil {
		return err
	}
	return s.health.Shutdown(ctx)
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
