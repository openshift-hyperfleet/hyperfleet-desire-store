/*
Package config resolves the Desire Store API configuration from command-line
flags, environment variables, and the documented defaults, in that order of
precedence.

Every setting follows the HyperFleet configuration standard: flags are
kebab-case and grouped by section, environment variables are prefixed with
HYPERFLEET_ and mirrored from the setting path.
*/
package config

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"time"

	hfl "github.com/openshift-hyperfleet/hyperfleet-logger"
	"github.com/spf13/pflag"
)

// Environment variables backing each setting. A flag always wins over its
// variable, and the variable wins over the default in Default.
const (
	envServerAddr      = "HYPERFLEET_SERVER_ADDR"
	envHealthAddr      = "HYPERFLEET_HEALTH_ADDR"
	envShutdownTimeout = "SHUTDOWN_TIMEOUT"
	envLogLevel        = "HYPERFLEET_LOG_LEVEL"  //nolint:gosec // not a credential
	envLogFormat       = "HYPERFLEET_LOG_FORMAT" //nolint:gosec // not a credential
	envLogOutput       = "HYPERFLEET_LOG_OUTPUT" //nolint:gosec // not a credential
)

// Defaults applied when neither a flag nor an environment variable is set.
const (
	defaultServerAddr      = ":8000"
	defaultHealthAddr      = ":8080"
	defaultShutdownTimeout = 20 * time.Second
	defaultLogLevel        = "info"
	defaultLogFormat       = "json"
	defaultLogOutput       = "stdout"
)

// Config holds every setting the service reads at startup.
type Config struct {
	// ServerAddr is the listen address of the Desire Store API data plane.
	ServerAddr string
	// HealthAddr is the listen address of the liveness and readiness
	// endpoints, which the standard fixes at 8080.
	HealthAddr string
	// ShutdownTimeout bounds the whole graceful shutdown, drain included. It
	// must stay below the pod's terminationGracePeriodSeconds.
	ShutdownTimeout time.Duration
	// LogLevel, LogFormat, and LogOutput configure the shared HyperFleet
	// logger.
	LogLevel  string
	LogFormat string
	LogOutput string
}

// Default returns the configuration with the environment applied on top of
// the documented defaults. An invalid value fails here instead of being
// silently ignored, so the service never starts with a value it did not
// understand.
func Default() (Config, error) {
	shutdownTimeout, err := durationFromEnv(envShutdownTimeout, defaultShutdownTimeout)
	if err != nil {
		return Config{}, err
	}

	return Config{
		ServerAddr:      stringFromEnv(envServerAddr, defaultServerAddr),
		HealthAddr:      stringFromEnv(envHealthAddr, defaultHealthAddr),
		ShutdownTimeout: shutdownTimeout,
		LogLevel:        stringFromEnv(envLogLevel, defaultLogLevel),
		LogFormat:       stringFromEnv(envLogFormat, defaultLogFormat),
		LogOutput:       stringFromEnv(envLogOutput, defaultLogOutput),
	}, nil
}

// BindFlags registers every setting on flags, using the values already in c as
// flag defaults. Parsing the flags therefore leaves an unset flag at its
// environment or default value and overrides it only when the operator sets
// one explicitly.
func (c *Config) BindFlags(flags *pflag.FlagSet) {
	flags.StringVar(&c.ServerAddr, "server-addr", c.ServerAddr,
		"listen address of the Desire Store API data plane ("+envServerAddr+")")
	flags.StringVar(&c.HealthAddr, "health-addr", c.HealthAddr,
		"listen address of the /healthz and /readyz endpoints ("+envHealthAddr+")")
	flags.DurationVar(&c.ShutdownTimeout, "shutdown-timeout", c.ShutdownTimeout,
		"maximum time for graceful shutdown, drain included ("+envShutdownTimeout+")")
	flags.StringVarP(&c.LogLevel, "log-level", "l", c.LogLevel,
		"minimum log level: debug, info, warn, error ("+envLogLevel+")")
	flags.StringVarP(&c.LogFormat, "log-format", "f", c.LogFormat,
		"log format: json, text ("+envLogFormat+")")
	flags.StringVar(&c.LogOutput, "log-output", c.LogOutput,
		"log destination: stdout, stderr ("+envLogOutput+")")
}

// Validate rejects a configuration the service cannot run with.
func (c Config) Validate() error {
	if c.ServerAddr == "" {
		return fmt.Errorf("server-addr must not be empty")
	}
	if c.HealthAddr == "" {
		return fmt.Errorf("health-addr must not be empty")
	}
	if c.ShutdownTimeout <= 0 {
		return fmt.Errorf("shutdown-timeout must be positive, got %s", c.ShutdownTimeout)
	}
	if _, err := c.logOptions(); err != nil {
		return err
	}
	return nil
}

// Logger builds the HyperFleet logger for this service. The component name and
// version are attached to every record.
func (c Config) Logger(component, version string) (*slog.Logger, error) {
	options, err := c.logOptions()
	if err != nil {
		return nil, err
	}
	return slog.New(hfl.NewHandler(component, version, options...)), nil
}

func (c Config) logOptions() ([]hfl.Option, error) {
	level, err := hfl.ParseLevel(c.LogLevel)
	if err != nil {
		return nil, fmt.Errorf("invalid log-level %q: %w", c.LogLevel, err)
	}
	format, err := hfl.ParseFormat(c.LogFormat)
	if err != nil {
		return nil, fmt.Errorf("invalid log-format %q: %w", c.LogFormat, err)
	}
	output, err := hfl.ParseOutput(c.LogOutput)
	if err != nil {
		return nil, fmt.Errorf("invalid log-output %q: %w", c.LogOutput, err)
	}
	return []hfl.Option{
		hfl.WithLevel(level),
		hfl.WithFormat(format),
		hfl.WithOutput(output),
	}, nil
}

func stringFromEnv(name, fallback string) string {
	if value, ok := os.LookupEnv(name); ok && value != "" {
		return value
	}
	return fallback
}

func durationFromEnv(name string, fallback time.Duration) (time.Duration, error) {
	value, ok := os.LookupEnv(name)
	if !ok || value == "" {
		return fallback, nil
	}
	// Accept a bare number of seconds as well as a Go duration string, so
	// SHUTDOWN_TIMEOUT=20 works next to SHUTDOWN_TIMEOUT=20s.
	if seconds, err := strconv.Atoi(value); err == nil {
		return time.Duration(seconds) * time.Second, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("invalid %s %q: %w", name, value, err)
	}
	return parsed, nil
}
