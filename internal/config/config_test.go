package config

import (
	"testing"
	"time"

	"github.com/spf13/pflag"
)

func TestDefaultUsesDocumentedValues(t *testing.T) {
	cfg, err := Default()
	if err != nil {
		t.Fatalf("Default() error: %v", err)
	}

	if cfg.ServerAddr != defaultServerAddr {
		t.Fatalf("ServerAddr = %q, want %q", cfg.ServerAddr, defaultServerAddr)
	}
	if cfg.HealthAddr != defaultHealthAddr {
		t.Fatalf("HealthAddr = %q, want %q", cfg.HealthAddr, defaultHealthAddr)
	}
	if cfg.ShutdownTimeout != defaultShutdownTimeout {
		t.Fatalf("ShutdownTimeout = %s, want %s", cfg.ShutdownTimeout, defaultShutdownTimeout)
	}
	if cfg.LogLevel != defaultLogLevel || cfg.LogFormat != defaultLogFormat {
		t.Fatalf("log settings = %q/%q, want %q/%q",
			cfg.LogLevel, cfg.LogFormat, defaultLogLevel, defaultLogFormat)
	}
}

func TestDefaultReadsEnvironment(t *testing.T) {
	t.Setenv(envServerAddr, ":9001")
	t.Setenv(envHealthAddr, ":9002")
	t.Setenv(envShutdownTimeout, "45s")
	t.Setenv(envLogLevel, "debug")

	cfg, err := Default()
	if err != nil {
		t.Fatalf("Default() error: %v", err)
	}

	if cfg.ServerAddr != ":9001" {
		t.Fatalf("ServerAddr = %q, want :9001", cfg.ServerAddr)
	}
	if cfg.HealthAddr != ":9002" {
		t.Fatalf("HealthAddr = %q, want :9002", cfg.HealthAddr)
	}
	if cfg.ShutdownTimeout != 45*time.Second {
		t.Fatalf("ShutdownTimeout = %s, want 45s", cfg.ShutdownTimeout)
	}
	if cfg.LogLevel != "debug" {
		t.Fatalf("LogLevel = %q, want debug", cfg.LogLevel)
	}
}

func TestDefaultAcceptsBareSeconds(t *testing.T) {
	t.Setenv(envShutdownTimeout, "30")

	cfg, err := Default()
	if err != nil {
		t.Fatalf("Default() error: %v", err)
	}

	if cfg.ShutdownTimeout != 30*time.Second {
		t.Fatalf("ShutdownTimeout = %s, want 30s", cfg.ShutdownTimeout)
	}
}

func TestDefaultRejectsMalformedDuration(t *testing.T) {
	t.Setenv(envShutdownTimeout, "twenty")

	if _, err := Default(); err == nil {
		t.Fatal("Default() must fail on a malformed SHUTDOWN_TIMEOUT")
	}
}

func TestFlagsOverrideTheEnvironment(t *testing.T) {
	t.Setenv(envServerAddr, ":9001")

	cfg, err := Default()
	if err != nil {
		t.Fatalf("Default() error: %v", err)
	}

	flags := pflag.NewFlagSet("serve", pflag.ContinueOnError)
	cfg.BindFlags(flags)
	if err := flags.Parse([]string{
		"--server-addr=:7000",
		"--health-addr=:7080",
		"--log-format=text",
	}); err != nil {
		t.Fatalf("parsing flags: %v", err)
	}

	if cfg.ServerAddr != ":7000" {
		t.Fatalf("ServerAddr = %q, want the flag value :7000", cfg.ServerAddr)
	}
	if cfg.HealthAddr != ":7080" {
		t.Fatalf("HealthAddr = %q, want the flag value :7080", cfg.HealthAddr)
	}
	if cfg.LogFormat != "text" {
		t.Fatalf("LogFormat = %q, want the flag value text", cfg.LogFormat)
	}
}

func TestValidateRejectsBadValues(t *testing.T) {
	tests := map[string]func(*Config){
		"empty server address":     func(c *Config) { c.ServerAddr = "" },
		"empty health address":     func(c *Config) { c.HealthAddr = "" },
		"zero shutdown timeout":    func(c *Config) { c.ShutdownTimeout = 0 },
		"unknown log level":        func(c *Config) { c.LogLevel = "verbose" },
		"unknown log format":       func(c *Config) { c.LogFormat = "xml" },
		"unknown log output":       func(c *Config) { c.LogOutput = "syslog" },
		"negative shutdown window": func(c *Config) { c.ShutdownTimeout = -time.Second },
	}

	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			cfg, err := Default()
			if err != nil {
				t.Fatalf("Default() error: %v", err)
			}
			mutate(&cfg)

			if err := cfg.Validate(); err == nil {
				t.Fatalf("Validate() accepted an invalid configuration: %+v", cfg)
			}
		})
	}
}

func TestValidateAcceptsDefaults(t *testing.T) {
	cfg, err := Default()
	if err != nil {
		t.Fatalf("Default() error: %v", err)
	}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() rejected the defaults: %v", err)
	}
}

func TestLoggerCarriesComponentAndVersion(t *testing.T) {
	cfg, err := Default()
	if err != nil {
		t.Fatalf("Default() error: %v", err)
	}

	logger, err := cfg.Logger("hyperfleet-desire-store", "v0.0.1")
	if err != nil {
		t.Fatalf("Logger() error: %v", err)
	}
	if logger == nil {
		t.Fatal("Logger() returned nil")
	}
}
