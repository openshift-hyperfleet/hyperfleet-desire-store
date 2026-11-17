package handlers

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	hfl "github.com/openshift-hyperfleet/hyperfleet-logger"

	"github.com/openshift-hyperfleet/hyperfleet-desire-store/internal/errors"
)

const (
	jsonContentType    = "application/json"
	problemContentType = "application/problem+json"
	timestampLayout    = "2006-01-02T15:04:05.000Z07:00"
)

// handleError logs a failed request and writes it as an RFC 9457 problem. A
// server-side failure is logged with its cause at error level; a client error
// is an expected condition, which the logging specification logs below it.
func handleError(w http.ResponseWriter, r *http.Request, log *slog.Logger, err *errors.ServiceError) {
	ctx := r.Context()
	attrs := []any{
		"status", err.HTTPCode, "error_code", err.Code, "error_type", errors.TypeBase + err.Type.Slug,
		"error", err.Reason, "method", r.Method, "path", r.URL.EscapedPath(),
	}
	if err.HTTPCode >= http.StatusInternalServerError {
		log.ErrorContext(ctx, "request failed", append(attrs, "cause", err.Cause)...)
	} else {
		log.InfoContext(ctx, "request rejected", attrs...)
	}

	traceID, _ := hfl.TraceIDFromContext(ctx)
	problem := err.AsProblem(r.URL.EscapedPath(), time.Now().UTC().Format(timestampLayout), traceID)
	writeBody(w, r, log, err.HTTPCode, problemContentType, problem)
}

// writeBody writes body with the status and content type. A marshal failure
// is a programming error in a fixed response type; a failed write is logged
// because the status line is already sent by then.
func writeBody(w http.ResponseWriter, r *http.Request, log *slog.Logger, status int, contentType string, body any) {
	payload, err := json.Marshal(body)
	if err != nil {
		log.ErrorContext(r.Context(), "encoding response", "error", err)
		w.Header().Set("Content-Type", problemContentType)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(status)
	if _, err := w.Write(payload); err != nil {
		log.ErrorContext(r.Context(), "writing response", "error", err)
	}
}
