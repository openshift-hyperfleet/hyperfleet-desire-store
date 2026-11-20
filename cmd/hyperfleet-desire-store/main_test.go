package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMainReportsStartupErrors(t *testing.T) {
	const serveCommand, validTimeout = "serve", "20s"

	t.Setenv("SHUTDOWN_TIMEOUT", "20s")
	t.Setenv("HYPERFLEET_LOG_LEVEL", "info")
	t.Setenv("HYPERFLEET_LOG_FORMAT", "json")
	t.Setenv("HYPERFLEET_LOG_OUTPUT", "stdout")
	t.Setenv("HYPERFLEET_SERVER_ADDR", ":8000")
	t.Setenv("HYPERFLEET_HEALTH_ADDR", ":8080")
	t.Setenv("HYPERFLEET_METRICS_ADDR", ":9090")
	// An empty variable reads as unset, so a partition claim in the
	// developer's shell cannot leak into the missing-claim case.
	t.Setenv("HYPERFLEET_PARTITION_CLAIM", "")

	binary := filepath.Join(t.TempDir(), "hyperfleet-desire-store")
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if output, err := exec.CommandContext(ctx, "go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("building command: %v\n%s", err, output)
	}

	tests := []struct {
		name    string
		timeout string
		args    []string
		wantErr string
	}{
		{
			name:    "malformed environment duration",
			timeout: "twenty",
			args:    []string{serveCommand},
			wantErr: "invalid SHUTDOWN_TIMEOUT",
		},
		{
			name:    "invalid log level",
			timeout: validTimeout,
			args:    []string{serveCommand, "--log-level=verbose"},
			wantErr: "invalid log-level",
		},
		{
			name:    "missing partition claim",
			timeout: validTimeout,
			args:    []string{serveCommand},
			wantErr: "partition-claim is required",
		},
		{
			name:    "successful version command",
			timeout: validTimeout,
			args:    []string{"version"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("SHUTDOWN_TIMEOUT", tt.timeout)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, binary, tt.args...)
			cmd.Env = os.Environ()
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			err := cmd.Run()
			if ctx.Err() != nil {
				t.Fatalf("command did not finish: %v", ctx.Err())
			}
			if tt.wantErr == "" {
				if err != nil || stderr.Len() != 0 || !strings.Contains(stdout.String(), "Version:") {
					t.Fatalf("version: err=%v stdout=%q stderr=%q", err, stdout.String(), stderr.String())
				}
				return
			}
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
				t.Fatalf("error = %v, want exit code 1", err)
			}
			if stdout.Len() != 0 {
				t.Fatalf("stdout = %q, want empty", stdout.String())
			}
			diagnostic := stderr.String()
			if !strings.Contains(diagnostic, tt.wantErr) ||
				strings.Count(diagnostic, "\n") != 1 || !strings.HasPrefix(diagnostic, "error: ") {
				t.Fatalf("stderr = %q, want one diagnostic containing %q", diagnostic, tt.wantErr)
			}
		})
	}
}
