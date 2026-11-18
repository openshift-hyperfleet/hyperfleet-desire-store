package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/openshift-hyperfleet/hyperfleet-desire-store/internal/config"
	"github.com/openshift-hyperfleet/hyperfleet-desire-store/internal/server"
)

// componentName is attached to every log record by the shared logger.
const componentName = "hyperfleet-desire-store"

func newServeCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the Desire Store API server",
	}

	cfg := config.Default()
	cfg.BindFlags(cmd.Flags())
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		// A malformed environment fails the command instead of being ignored.
		if err := config.ApplyEnv(cmd.Flags()); err != nil {
			cmd.SilenceUsage = true
			return fmt.Errorf("invalid configuration: %w", err)
		}
		return serve(cmd.Context(), cfg)
	}

	return cmd
}

// serve wires the configuration, logger, and servers together and blocks until
// a shutdown signal arrives.
func serve(ctx context.Context, cfg config.Config) error {
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("invalid configuration: %w", err)
	}

	logger, err := cfg.Logger(componentName, version)
	if err != nil {
		return fmt.Errorf("invalid configuration: %w", err)
	}
	slog.SetDefault(logger)

	// SIGTERM is what Kubernetes sends on rollout; SIGINT keeps Ctrl-C working
	// locally. Both start the same graceful shutdown.
	shutdownCtx, stop := signal.NotifyContext(ctx, syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	// The v1 routes are mounted here once the store is wired in; until then
	// every path is a 404 and the listener only matches the deployment shape.
	api := http.NewServeMux()
	opts := server.Options{Addr: cfg.ServerAddr, HealthAddr: cfg.HealthAddr, ShutdownTimeout: cfg.ShutdownTimeout}
	return server.New(api, opts, logger).Run(shutdownCtx)
}
