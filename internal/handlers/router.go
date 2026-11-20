// Package handlers serves the Desire Store REST v1 contract over HTTP. It
// decodes requests, calls the desire service, and presents the results; the
// business rules live in the services package.
package handlers

import (
	"fmt"
	"log/slog"
	"net/http"

	"github.com/openshift-hyperfleet/hyperfleet-desire-store/internal/auth"
	"github.com/openshift-hyperfleet/hyperfleet-desire-store/internal/metrics"
	"github.com/openshift-hyperfleet/hyperfleet-desire-store/internal/services"
)

// desiresPath is the REST v1 collection route.
const desiresPath = "/api/hyperfleet/desire-store/v1/partitions/{partition}/desires"

// NewRouter returns the data-plane handler: the REST v1 routes behind trace
// propagation, Wristband authentication, and validation against the REST v1
// contract. Every path, unknown ones included, requires a valid Wristband, so
// an unauthenticated request gets 401 before any validation. Every request is
// counted in m, which must be the instance whose Handler the metrics listener
// serves: a second metrics.New counts into a registry no listener exposes.
func NewRouter(
	service services.DesireService, validator *auth.Validator, m *metrics.Metrics, log *slog.Logger,
) (http.Handler, error) {
	if m == nil {
		return nil, fmt.Errorf("metrics must not be nil")
	}
	contract, err := parseContract()
	if err != nil {
		return nil, err
	}
	desires := NewDesireHandler(service, log)

	mux := http.NewServeMux()
	mux.HandleFunc("GET "+desiresPath, desires.List)
	mux.HandleFunc("GET "+desiresPath+"/{id}", desires.Get)
	mux.HandleFunc("PUT "+desiresPath+"/{id}", desires.Write)
	mux.HandleFunc("DELETE "+desiresPath+"/{id}", desires.Delete)
	mux.HandleFunc("PUT "+desiresPath+"/{id}/status", desires.WriteStatus)

	// Built from the inside out. Authentication runs before validation, so an
	// unauthenticated request gets 401 first. instrument is outermost, so it
	// also counts the requests the inner layers reject.
	handler := notFoundProblems(mux, log)
	handler = validateRequests(contract, log, handler)
	handler = authenticate(validator, log, handler)
	handler = traceContext(handler)
	return instrument(m, mux, handler), nil
}
