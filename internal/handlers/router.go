// Package handlers serves the Desire Store REST v1 contract over HTTP. It
// decodes requests, calls the desire service, and presents the results; the
// business rules live in the services package.
package handlers

import (
	"log/slog"
	"net/http"

	"github.com/openshift-hyperfleet/hyperfleet-desire-store/internal/auth"
	"github.com/openshift-hyperfleet/hyperfleet-desire-store/internal/services"
)

// desiresPath is the REST v1 collection route.
const desiresPath = "/api/hyperfleet/desire-store/v1/partitions/{partition}/desires"

// NewRouter returns the data-plane handler: the REST v1 routes behind trace
// propagation and Wristband authentication. Every path, unknown ones
// included, requires a valid Wristband.
func NewRouter(service services.DesireService, validator *auth.Validator, log *slog.Logger) http.Handler {
	desires := NewDesireHandler(service, log)

	mux := http.NewServeMux()
	mux.HandleFunc("GET "+desiresPath, desires.List)
	mux.HandleFunc("GET "+desiresPath+"/{id}", desires.Get)
	mux.HandleFunc("PUT "+desiresPath+"/{id}", desires.Write)
	mux.HandleFunc("DELETE "+desiresPath+"/{id}", desires.Delete)
	mux.HandleFunc("PUT "+desiresPath+"/{id}/status", desires.WriteStatus)

	return traceContext(authenticate(validator, log, problemsForUnmatched(mux, log)))
}
