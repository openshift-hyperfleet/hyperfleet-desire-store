package config

import (
	"strings"
	"testing"
	"time"

	"github.com/spf13/pflag"
)

// load resolves the configuration the way serve does: defaults, then flags,
// then the environment for every flag left unset.
func load(t *testing.T, args ...string) (Config, error) {
	t.Helper()
	cfg := Default()
	flags := pflag.NewFlagSet("serve", pflag.ContinueOnError)
	cfg.BindFlags(flags)
	if err := flags.Parse(args); err != nil {
		t.Fatalf("parsing flags: %v", err)
	}
	return cfg, ApplyEnv(flags)
}

// TestEnvironmentUsesTheFlagSyntax reads the algorithms from the environment
// into an otherwise valid configuration, so only their syntax decides.
func TestEnvironmentUsesTheFlagSyntax(t *testing.T) {
	tests := map[string]struct {
		value string
		valid bool
	}{
		"comma-separated":      {"ES256,ES384", true},
		"spaces in algorithms": {"ES256, ES384", false},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Setenv(envWristbandAlgorithms, tc.value)
			loaded, err := load(t)
			if err != nil {
				t.Fatalf("load() error: %v", err)
			}
			cfg := validConfig(t)
			cfg.WristbandAlgorithms = loaded.WristbandAlgorithms

			err = cfg.Validate()
			switch {
			case tc.valid && err != nil:
				t.Fatalf("%s=%q was rejected: %v", envWristbandAlgorithms, tc.value, err)
			case !tc.valid && (err == nil || !strings.Contains(err.Error(), settingAlgorithms)):
				t.Fatalf("%s=%q: error = %v, want one naming %s", envWristbandAlgorithms, tc.value, err, settingAlgorithms)
			}
		})
	}
}

func TestEnvironmentParsesShutdownTimeout(t *testing.T) {
	tests := map[string]struct {
		value string
		want  time.Duration
	}{
		"empty uses default": {"", defaultShutdownTimeout},
		"duration":           {"45s", 45 * time.Second},
		"bare seconds":       {"30", 30 * time.Second},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Setenv(envShutdownTimeout, tc.value)
			cfg, err := load(t)
			if err != nil {
				t.Fatalf("load() error: %v", err)
			}
			if cfg.ShutdownTimeout != tc.want {
				t.Fatalf("ShutdownTimeout = %s, want %s", cfg.ShutdownTimeout, tc.want)
			}
		})
	}
}

func TestMetricsAddrFromTheEnvironment(t *testing.T) {
	cfg, err := load(t)
	if err != nil {
		t.Fatalf("load() error: %v", err)
	}
	if cfg.MetricsAddr != defaultMetricsAddr {
		t.Fatalf("MetricsAddr = %q, want the default %q", cfg.MetricsAddr, defaultMetricsAddr)
	}

	t.Setenv(envMetricsAddr, ":9191")
	cfg, err = load(t)
	if err != nil {
		t.Fatalf("load() error: %v", err)
	}
	if cfg.MetricsAddr != ":9191" {
		t.Fatalf("MetricsAddr = %q, want the environment value", cfg.MetricsAddr)
	}
}

func TestDefaultRejectsMalformedDuration(t *testing.T) {
	t.Setenv(envShutdownTimeout, "twenty")

	if _, err := load(t); err == nil {
		t.Fatal("ApplyEnv() must fail on a malformed SHUTDOWN_TIMEOUT")
	}
}

const (
	settingJWKSURL         = "wristband-jwks-url"
	settingMaxConcurrency  = "db-max-concurrency"
	settingAlgorithms      = "wristband-algorithms"
	settingSweeperIdentity = "sweeper-identity"
)

// validConfig returns the defaults with every required setting filled, so a
// test that mutates one setting sees only that setting's error.
func validConfig(t *testing.T) Config {
	t.Helper()
	cfg := Default()
	cfg.PartitionClaim = "hf_partition"
	cfg.WristbandIssuer = "https://authorino.example/x-wristband"
	cfg.WristbandAudience = "desire-store-api"
	cfg.WristbandJWKSURL = "https://authorino.example/x-wristband/.well-known/openid-connect/certs"
	cfg.SweeperIdentity = "system:serviceaccount:hyperfleet:desire-sweeper"
	cfg.DBMaxConcurrency = 8
	return cfg
}

func TestValidateRejectsBadValues(t *testing.T) {
	tests := map[string]struct {
		mutate  func(*Config)
		setting string
	}{
		"empty server address":  {func(c *Config) { c.ServerAddr = "" }, "server-addr"},
		"empty health address":  {func(c *Config) { c.HealthAddr = "" }, "health-addr"},
		"empty metrics address": {func(c *Config) { c.MetricsAddr = "" }, "metrics-addr"},
		"zero shutdown timeout": {func(c *Config) { c.ShutdownTimeout = 0 }, "shutdown-timeout"},
		"unknown log level":     {func(c *Config) { c.LogLevel = "verbose" }, "log-level"},
		"unknown log format":    {func(c *Config) { c.LogFormat = "xml" }, "log-format"},
		"unknown log output":    {func(c *Config) { c.LogOutput = "syslog" }, "log-output"},
		"unparsable JWKS URL":   {func(c *Config) { c.WristbandJWKSURL = "https://[::1" }, settingJWKSURL},
		"relative JWKS URL":     {func(c *Config) { c.WristbandJWKSURL = "/certs" }, settingJWKSURL},
		"non-HTTP JWKS URL": {
			func(c *Config) { c.WristbandJWKSURL = "ftp://issuer.example/certs" }, settingJWKSURL,
		},
		"JWKS URL without a host": {func(c *Config) { c.WristbandJWKSURL = "https:///certs" }, settingJWKSURL},
		"JWKS URL with credentials": {
			func(c *Config) { c.WristbandJWKSURL = "https://user:secret@issuer.example/certs" }, settingJWKSURL,
		},
		"no algorithms":     {func(c *Config) { c.WristbandAlgorithms = nil }, settingAlgorithms},
		"unknown algorithm": {func(c *Config) { c.WristbandAlgorithms = []string{"XS999"} }, settingAlgorithms},
		"none algorithm":    {func(c *Config) { c.WristbandAlgorithms = []string{"none"} }, settingAlgorithms},
		"sweeper identity without the service-account prefix": {
			func(c *Config) { c.SweeperIdentity = "hyperfleet:desire-sweeper" }, settingSweeperIdentity,
		},
		"sweeper identity without a namespace": {
			func(c *Config) { c.SweeperIdentity = "system:serviceaccount::desire-sweeper" }, settingSweeperIdentity,
		},
		"sweeper identity with an invalid name": {
			func(c *Config) { c.SweeperIdentity = "system:serviceaccount:hyperfleet:Desire_Sweeper" }, settingSweeperIdentity,
		},
		"negative concurrency": {func(c *Config) { c.DBMaxConcurrency = -1 }, settingMaxConcurrency},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			cfg := validConfig(t)
			tc.mutate(&cfg)

			err := cfg.Validate()
			if err == nil {
				t.Fatalf("Validate() accepted an invalid configuration: %+v", cfg)
			}
			if !strings.Contains(err.Error(), tc.setting) {
				t.Fatalf("Validate() error %q does not name %s", err, tc.setting)
			}
			if strings.Contains(err.Error(), "secret") {
				t.Fatalf("Validate() error %q discloses the URL credentials", err)
			}
		})
	}
}

func TestValidateRequiresAccessSettings(t *testing.T) {
	tests := map[string]func(*Config){
		"partition-claim":      func(c *Config) { c.PartitionClaim = "" },
		"wristband-issuer":     func(c *Config) { c.WristbandIssuer = "" },
		"wristband-audience":   func(c *Config) { c.WristbandAudience = "" },
		settingJWKSURL:         func(c *Config) { c.WristbandJWKSURL = "" },
		settingSweeperIdentity: func(c *Config) { c.SweeperIdentity = "  " },
		settingMaxConcurrency:  func(c *Config) { c.DBMaxConcurrency = 0 },
	}

	for setting, unset := range tests {
		t.Run(setting, func(t *testing.T) {
			cfg := validConfig(t)
			unset(&cfg)

			err := cfg.Validate()
			if err == nil {
				t.Fatalf("Validate() accepted a configuration without %s", setting)
			}
			if !strings.Contains(err.Error(), setting) {
				t.Fatalf("Validate() error %q does not name %s", err, setting)
			}
		})
	}
}

func TestValidateRejectsDefaultsAlone(t *testing.T) {
	cfg := Default()

	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "partition-claim") {
		t.Fatalf("Validate() of the defaults = %v, want the missing partition-claim error", err)
	}
}

func TestValidateAcceptsCompleteConfiguration(t *testing.T) {
	cfg := validConfig(t)

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() rejected a complete configuration: %v", err)
	}
}

func TestDefaultReadsAccessSettings(t *testing.T) {
	t.Setenv(envPartitionClaim, "hf_partition")
	t.Setenv(envWristbandIssuer, "https://issuer.example")
	t.Setenv(envWristbandAudience, "desire-store-api")
	t.Setenv(envWristbandJWKSURL, "https://issuer.example/certs")
	t.Setenv(envWristbandJWKSCAFile, "/etc/ca.pem")
	t.Setenv(envWristbandAlgorithms, "ES256,ES384")
	t.Setenv(envSweeperIdentity, "system:serviceaccount:hyperfleet:desire-sweeper")
	t.Setenv(envDBMaxConcurrency, "12")

	cfg, err := load(t)
	if err != nil {
		t.Fatalf("load() error: %v", err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() rejected the environment configuration: %v", err)
	}

	if cfg.WristbandJWKSCAFile != "/etc/ca.pem" {
		t.Fatalf("WristbandJWKSCAFile = %q, want /etc/ca.pem", cfg.WristbandJWKSCAFile)
	}
	if strings.Join(cfg.WristbandAlgorithms, ",") != "ES256,ES384" {
		t.Fatalf("WristbandAlgorithms = %q, want [ES256 ES384]", cfg.WristbandAlgorithms)
	}
	if cfg.DBMaxConcurrency != 12 {
		t.Fatalf("DBMaxConcurrency = %d, want 12", cfg.DBMaxConcurrency)
	}
}

func TestAccessFlagsOverrideTheEnvironment(t *testing.T) {
	t.Setenv(envPartitionClaim, "env_claim")
	t.Setenv(envWristbandAlgorithms, "RS256")
	t.Setenv(envDBMaxConcurrency, "many")

	// The malformed concurrency variable is ignored because its flag is set.
	cfg, err := load(t,
		"--partition-claim=flag_claim",
		"--wristband-algorithms=ES384",
		"--db-max-concurrency=3",
	)
	if err != nil {
		t.Fatalf("load() error: %v", err)
	}

	if cfg.PartitionClaim != "flag_claim" {
		t.Fatalf("PartitionClaim = %q, want the flag value flag_claim", cfg.PartitionClaim)
	}
	if strings.Join(cfg.WristbandAlgorithms, ",") != "ES384" {
		t.Fatalf("WristbandAlgorithms = %v, want [ES384]", cfg.WristbandAlgorithms)
	}
	if cfg.DBMaxConcurrency != 3 {
		t.Fatalf("DBMaxConcurrency = %d, want 3", cfg.DBMaxConcurrency)
	}
}
