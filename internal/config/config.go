/*
Package config resolves the Desire Store API configuration from command-line
flags, environment variables, and the documented defaults, in that order of
precedence.

Every setting follows the HyperFleet configuration standard: flags are
kebab-case and grouped by section, environment variables are prefixed with
HYPERFLEET_ and mirrored from the setting path, except SHUTDOWN_TIMEOUT,
which is unprefixed as defined by the graceful shutdown standard.
*/
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	hfl "github.com/openshift-hyperfleet/hyperfleet-logger"
	"github.com/spf13/pflag"
	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/openshift-hyperfleet/hyperfleet-desire-store/internal/auth"
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

	envPartitionClaim      = "HYPERFLEET_PARTITION_CLAIM"
	envWristbandIssuer     = "HYPERFLEET_WRISTBAND_ISSUER"
	envWristbandAudience   = "HYPERFLEET_WRISTBAND_AUDIENCE"
	envWristbandJWKSURL    = "HYPERFLEET_WRISTBAND_JWKS_URL"
	envWristbandJWKSCAFile = "HYPERFLEET_WRISTBAND_JWKS_CA_FILE"
	envWristbandAlgorithms = "HYPERFLEET_WRISTBAND_ALGORITHMS"
	envSweeperIdentity     = "HYPERFLEET_SWEEPER_IDENTITY"
	envDBMaxConcurrency    = "HYPERFLEET_DB_MAX_CONCURRENCY"
)

// Defaults applied when neither a flag nor an environment variable is set.
const (
	defaultServerAddr      = ":8000"
	defaultHealthAddr      = ":8080"
	defaultShutdownTimeout = 20 * time.Second
	defaultLogLevel        = "info"
	defaultLogFormat       = "json"
	defaultLogOutput       = "stdout"

	defaultWristbandAlgorithm = "ES256"
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

	// PartitionClaim names the Wristband claim that carries the caller's
	// authorized partition. It is required and has no default, so partition
	// enforcement cannot be left unconfigured (ADR-0022).
	PartitionClaim string
	// WristbandIssuer and WristbandAudience are the iss and aud values every
	// Wristband must carry.
	WristbandIssuer   string
	WristbandAudience string
	// WristbandJWKSURL is the key set that signs Wristbands, and
	// WristbandJWKSCAFile optionally adds a CA to trust when fetching it.
	WristbandJWKSURL    string
	WristbandJWKSCAFile string
	// WristbandAlgorithms lists the accepted signing algorithms.
	WristbandAlgorithms []string
	// SweeperIdentity is the hf_identity of the centralized sweeper. A
	// fleet-scoped caller with any other identity is an Adapter.
	SweeperIdentity string
	// DBMaxConcurrency caps the database connections of this process. The
	// Postgres store sizes its connection pool with it.
	DBMaxConcurrency int
}

// Default returns the documented defaults. The environment and the flags are
// applied on top of them by BindFlags and ApplyEnv.
func Default() Config {
	return Config{
		ServerAddr:          defaultServerAddr,
		HealthAddr:          defaultHealthAddr,
		ShutdownTimeout:     defaultShutdownTimeout,
		LogLevel:            defaultLogLevel,
		LogFormat:           defaultLogFormat,
		LogOutput:           defaultLogOutput,
		WristbandAlgorithms: []string{defaultWristbandAlgorithm},
	}
}

// flagEnv maps each flag to the environment variable that backs it.
var flagEnv = map[string]string{
	"server-addr":            envServerAddr,
	"health-addr":            envHealthAddr,
	"shutdown-timeout":       envShutdownTimeout,
	"log-level":              envLogLevel,
	"log-format":             envLogFormat,
	"log-output":             envLogOutput,
	"partition-claim":        envPartitionClaim,
	"wristband-issuer":       envWristbandIssuer,
	"wristband-audience":     envWristbandAudience,
	"wristband-jwks-url":     envWristbandJWKSURL,
	"wristband-jwks-ca-file": envWristbandJWKSCAFile,
	"wristband-algorithms":   envWristbandAlgorithms,
	"sweeper-identity":       envSweeperIdentity,
	"db-max-concurrency":     envDBMaxConcurrency,
}

// BindFlags registers every setting on flags, using the values already in c as
// flag defaults, and names the backing environment variable in each usage.
func (c *Config) BindFlags(flags *pflag.FlagSet) {
	flags.StringVar(&c.ServerAddr, "server-addr", c.ServerAddr,
		"listen address of the Desire Store API data plane")
	flags.StringVar(&c.HealthAddr, "health-addr", c.HealthAddr,
		"listen address of the /healthz and /readyz endpoints")
	flags.DurationVar(&c.ShutdownTimeout, "shutdown-timeout", c.ShutdownTimeout,
		"maximum time for graceful shutdown, drain included")
	flags.StringVarP(&c.LogLevel, "log-level", "l", c.LogLevel,
		"minimum log level: debug, info, warn, error")
	flags.StringVarP(&c.LogFormat, "log-format", "f", c.LogFormat,
		"log format: json, text")
	flags.StringVar(&c.LogOutput, "log-output", c.LogOutput,
		"log destination: stdout, stderr")
	flags.StringVar(&c.PartitionClaim, "partition-claim", c.PartitionClaim,
		"Wristband claim that carries the caller's partition, required")
	flags.StringVar(&c.WristbandIssuer, "wristband-issuer", c.WristbandIssuer,
		"required iss claim of every Wristband")
	flags.StringVar(&c.WristbandAudience, "wristband-audience", c.WristbandAudience,
		"required aud claim of every Wristband")
	flags.StringVar(&c.WristbandJWKSURL, "wristband-jwks-url", c.WristbandJWKSURL,
		"URL of the JWKS that signs Wristbands, required")
	flags.StringVar(&c.WristbandJWKSCAFile, "wristband-jwks-ca-file", c.WristbandJWKSCAFile,
		"PEM CA bundle trusted when fetching the JWKS, optional")
	flags.StringSliceVar(&c.WristbandAlgorithms, "wristband-algorithms", c.WristbandAlgorithms,
		"accepted Wristband signing algorithms")
	flags.StringVar(&c.SweeperIdentity, "sweeper-identity", c.SweeperIdentity,
		"hf_identity of the centralized sweeper, required")
	flags.IntVar(&c.DBMaxConcurrency, "db-max-concurrency", c.DBMaxConcurrency,
		"maximum database connections of this process, required")

	for name, env := range flagEnv {
		f := flags.Lookup(name)
		f.Usage += " (" + env + ")"
	}
}

// ApplyEnv sets every flag that was not given on the command line from its
// environment variable, parsing the value exactly as the flag would. Call it
// after the flags are parsed: a flag wins over its variable, and an unset or
// empty variable leaves the default in place. An invalid value fails here
// instead of being silently ignored, so the service never starts with a value
// it did not understand.
func ApplyEnv(flags *pflag.FlagSet) error {
	var errs []error
	flags.VisitAll(func(f *pflag.Flag) {
		env, ok := flagEnv[f.Name]
		if !ok || f.Changed {
			return
		}
		value := os.Getenv(env)
		if value == "" {
			return
		}
		// The graceful shutdown standard writes SHUTDOWN_TIMEOUT as a bare
		// number of seconds; a Go duration string works too.
		if _, err := strconv.Atoi(value); err == nil && env == envShutdownTimeout {
			value += "s"
		}
		if err := f.Value.Set(value); err != nil {
			errs = append(errs, fmt.Errorf("invalid %s %q: %w", env, value, err))
		}
	})
	return errors.Join(errs...)
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
	return c.validateAccess()
}

// validateAccess checks the settings that authentication, authorization, and
// the database connection cap depend on. None of them can be switched off: a
// missing value stops the service before it serves a request.
func (c Config) validateAccess() error {
	if err := c.Wristband().Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(c.SweeperIdentity) == "" {
		return fmt.Errorf("sweeper-identity is required")
	}
	if err := validateServiceAccount(c.SweeperIdentity); err != nil {
		return fmt.Errorf("invalid sweeper-identity %q: %w", c.SweeperIdentity, err)
	}
	if c.DBMaxConcurrency < 1 {
		return fmt.Errorf("db-max-concurrency must be at least 1, got %d", c.DBMaxConcurrency)
	}
	return nil
}

// validateServiceAccount checks that identity is a service-account username,
// system:serviceaccount:<namespace>:<name>, the form hf_identity takes. Every
// fleet-scoped caller that is not the sweeper gets the Adapter's permissions,
// so a malformed sweeper identity would silently give the sweeper those.
func validateServiceAccount(identity string) error {
	rest, ok := strings.CutPrefix(identity, "system:serviceaccount:")
	namespace, name, found := strings.Cut(rest, ":")
	if !ok || !found {
		return errors.New("must be system:serviceaccount:<namespace>:<name>")
	}
	if errs := validation.IsDNS1123Label(namespace); len(errs) > 0 {
		return fmt.Errorf("namespace %q: %s", namespace, strings.Join(errs, "; "))
	}
	if errs := validation.IsDNS1123Subdomain(name); len(errs) > 0 {
		return fmt.Errorf("name %q: %s", name, strings.Join(errs, "; "))
	}
	return nil
}

// Wristband returns the settings that Wristband validation uses.
func (c Config) Wristband() auth.Options {
	return auth.Options{
		Issuer:         c.WristbandIssuer,
		Audience:       c.WristbandAudience,
		JWKSURL:        c.WristbandJWKSURL,
		JWKSCAFile:     c.WristbandJWKSCAFile,
		Algorithms:     c.WristbandAlgorithms,
		PartitionClaim: c.PartitionClaim,
	}
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
