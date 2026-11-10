package server

import (
	"context"
	"encoding/json"
	"net/http"
)

// Probe status values and the check names reported by /readyz.
const (
	statusOK    = "ok"
	statusError = "error"

	checkConfig = "config"
)

// probeResponse is the body of /healthz and /readyz. The health-endpoints
// standard defines these two shapes: liveness reports only the status, and
// readiness adds the per-check detail.
type probeResponse struct {
	Status  string            `json:"status"`
	Checks  map[string]string `json:"checks,omitempty"`
	Message string            `json:"message,omitempty"`
}

// handleLiveness reports that the process can serve HTTP. It deliberately
// checks no dependency: a database outage must not restart the pod.
func (s *Server) handleLiveness(w http.ResponseWriter, r *http.Request) {
	s.writeJSON(r.Context(), w, http.StatusOK, probeResponse{Status: statusOK})
}

// handleReadiness reports whether the pod may receive traffic. It returns 503
// during startup and throughout the shutdown drain, which is what removes the
// pod from service endpoints.
func (s *Server) handleReadiness(w http.ResponseWriter, r *http.Request) {
	checks := map[string]string{checkConfig: statusOK}

	if !s.ready.Load() {
		s.writeJSON(r.Context(), w, http.StatusServiceUnavailable, probeResponse{
			Status:  statusError,
			Checks:  checks,
			Message: "not ready",
		})
		return
	}

	s.writeJSON(r.Context(), w, http.StatusOK, probeResponse{Status: statusOK, Checks: checks})
}

// writeJSON writes body as JSON. A marshal failure is a programming error in a
// fixed response struct, so the 500 fallback only keeps the connection sane; a
// failed write is logged because by then the status line is already sent.
func (s *Server) writeJSON(ctx context.Context, w http.ResponseWriter, code int, body probeResponse) {
	payload, err := json.Marshal(body)
	if err != nil {
		s.log.ErrorContext(ctx, "encoding probe response", "error", err)
		http.Error(w, `{"status":"error","message":"response encoding failed"}`,
			http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if _, err := w.Write(payload); err != nil {
		s.log.ErrorContext(ctx, "writing probe response", "error", err)
	}
}
