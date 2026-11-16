package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestShutdownErrorLogLevel(t *testing.T) {
	tests := []struct {
		name  string
		err   error
		level slog.Level
	}{
		{name: "timeout", err: context.DeadlineExceeded, level: slog.LevelWarn},
		{name: "wrapped timeout", err: fmt.Errorf("api drain: %w", context.DeadlineExceeded), level: slog.LevelWarn},
		{name: "failure", err: errors.New("listener close failed"), level: slog.LevelError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var logs bytes.Buffer
			server := newTestServer(t)
			server.log = slog.New(slog.NewJSONHandler(&logs, nil))

			server.logShutdownError(context.Background(), "shutdown did not complete", tt.err)

			var entry struct {
				Level string `json:"level"`
				Error string `json:"error"`
			}
			if err := json.Unmarshal(logs.Bytes(), &entry); err != nil {
				t.Fatalf("decoding shutdown log: %v", err)
			}
			if entry.Level != tt.level.String() || entry.Error != tt.err.Error() {
				t.Fatalf("log = %+v, want level %s and error %q", entry, tt.level, tt.err)
			}
		})
	}
}

type disconnectedResponseWriter struct {
	*httptest.ResponseRecorder
}

func (w disconnectedResponseWriter) Write([]byte) (int, error) {
	return 0, io.ErrClosedPipe
}

func TestProbeWriteFailureLogsWarning(t *testing.T) {
	var logs bytes.Buffer
	server := newTestServer(t)
	server.log = slog.New(slog.NewJSONHandler(&logs, nil))
	recorder := httptest.NewRecorder()

	server.handleLiveness(disconnectedResponseWriter{recorder},
		httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	var entry struct {
		Level   string `json:"level"`
		Message string `json:"msg"`
		Error   string `json:"error"`
	}
	if err := json.Unmarshal(logs.Bytes(), &entry); err != nil {
		t.Fatalf("decoding probe write log: %v", err)
	}
	if entry.Level != slog.LevelWarn.String() ||
		entry.Message != "writing probe response" || entry.Error != io.ErrClosedPipe.Error() {
		t.Fatalf("unexpected probe write log: %+v", entry)
	}
}
