package main

import (
	"context"
	"fmt"
	"log/slog"
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

	cfg, err := config.Default()
	if err != nil {
		// A malformed environment fails the command instead of being ignored.
		// Flags are not bound in this case, but --help still works.
		cmd.SilenceUsage = true
		cmd.RunE = func(*cobra.Command, []string) error {
			return fmt.Errorf("invalid configuration: %w", err)
		}
		return cmd
	}

	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		return serve(cmd.Context(), cfg)
	}
	cfg.BindFlags(cmd.Flags())

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

	return server.New(cfg, logger).Run(shutdownCtx)
}
