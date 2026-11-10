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

	"github.com/openshift-hyperfleet/hyperfleet-desire-store/internal/config"
)

// Timeouts applied to both listeners.
const (
	readHeaderTimeout = 10 * time.Second
	healthReadTimeout = 5 * time.Second
)

// Server serves the Desire Store API data plane and its health endpoints.
type Server struct {
	cfg    config.Config
	log    *slog.Logger
	ready  atomic.Bool
	api    *http.Server
	health *http.Server
}

// New builds both listeners. Nothing is bound until Run is called.
func New(cfg config.Config, log *slog.Logger) *Server {
	server := &Server{cfg: cfg, log: log}
	server.api = &http.Server{
		Addr:              cfg.ServerAddr,
		Handler:           server.apiHandler(),
		ReadHeaderTimeout: readHeaderTimeout,
	}
	server.health = &http.Server{
		Addr:              cfg.HealthAddr,
		Handler:           server.healthHandler(),
		ReadHeaderTimeout: healthReadTimeout,
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
		"server_addr", s.cfg.ServerAddr,
		"health_addr", s.cfg.HealthAddr,
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
			s.log.ErrorContext(ctx, "shutdown after a listener failure did not complete",
				"error", shutdownErr)
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
	ctx, cancel := context.WithTimeout(context.Background(), s.cfg.ShutdownTimeout)
	defer cancel()

	if err := s.Shutdown(ctx); err != nil {
		s.log.ErrorContext(ctx, "graceful shutdown did not complete within the timeout",
			"shutdown_timeout", s.cfg.ShutdownTimeout, "error", err)
		return err
	}

	s.log.InfoContext(ctx, "shutdown complete")
	return nil
}

// apiHandler builds the data-plane routes. The v1 Desire Store routes are
// added by the service implementation; until then every path is a 404 and the
// listener exists only so the deployment shape matches the design.
func (s *Server) apiHandler() http.Handler {
	return http.NewServeMux()
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
