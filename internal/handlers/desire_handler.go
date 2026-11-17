package handlers

import (
	"log/slog"
	"net/http"

	"github.com/openshift-hyperfleet/hyperfleet-desire-store/internal/auth"
	"github.com/openshift-hyperfleet/hyperfleet-desire-store/internal/errors"
	"github.com/openshift-hyperfleet/hyperfleet-desire-store/internal/presenters"
	"github.com/openshift-hyperfleet/hyperfleet-desire-store/internal/services"
)

// DesireHandler serves the five REST v1 operations. Each method decodes the
// request, calls the service, and presents the result.
type DesireHandler struct {
	service services.DesireService
	log     *slog.Logger
}

// NewDesireHandler serves the desire routes over the service.
func NewDesireHandler(service services.DesireService, log *slog.Logger) *DesireHandler {
	return &DesireHandler{service: service, log: log}
}

// List serves List desires.
func (h *DesireHandler) List(w http.ResponseWriter, r *http.Request) {
	query, err := decodeListQuery(r.URL.Query())
	if err != nil {
		handleError(w, r, h.log, err)
		return
	}
	caller, _ := auth.CallerFromContext(r.Context())
	page, err := h.service.List(r.Context(), caller, r.PathValue("partition"), query)
	if err != nil {
		handleError(w, r, h.log, err)
		return
	}
	list, encodeErr := presenters.PresentList(page)
	if encodeErr != nil {
		handleError(w, r, h.log, errors.Unavailable(errors.CodeDependencyFailure, "The desire list could not be paged.").
			WithCause(encodeErr))
		return
	}
	writeBody(w, r, h.log, http.StatusOK, jsonContentType, list)
}

// Get serves Get desire.
func (h *DesireHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := parseDesireID(r.PathValue("id"), r.PathValue("partition"))
	if err != nil {
		handleError(w, r, h.log, err)
		return
	}
	caller, _ := auth.CallerFromContext(r.Context())
	stored, err := h.service.Get(r.Context(), caller, id)
	if err != nil {
		handleError(w, r, h.log, err)
		return
	}
	writeBody(w, r, h.log, http.StatusOK, jsonContentType, presenters.PresentDesire(stored))
}

// Write serves Write desire: 201 for a create, 200 for an update.
func (h *DesireHandler) Write(w http.ResponseWriter, r *http.Request) {
	id, err := parseDesireID(r.PathValue("id"), r.PathValue("partition"))
	if err != nil {
		handleError(w, r, h.log, err)
		return
	}
	input, err := decodeWrite(r.Body, id)
	if err != nil {
		handleError(w, r, h.log, err)
		return
	}
	caller, _ := auth.CallerFromContext(r.Context())
	stored, created, err := h.service.Write(r.Context(), caller, id, input)
	if err != nil {
		handleError(w, r, h.log, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeBody(w, r, h.log, status, jsonContentType, presenters.PresentDesire(stored))
}

// WriteStatus serves Write status.
func (h *DesireHandler) WriteStatus(w http.ResponseWriter, r *http.Request) {
	id, err := parseDesireID(r.PathValue("id"), r.PathValue("partition"))
	if err != nil {
		handleError(w, r, h.log, err)
		return
	}
	input, err := decodeStatus(r.Body, id)
	if err != nil {
		handleError(w, r, h.log, err)
		return
	}
	caller, _ := auth.CallerFromContext(r.Context())
	if err := h.service.WriteStatus(r.Context(), caller, id, input); err != nil {
		handleError(w, r, h.log, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Delete serves Delete desire.
func (h *DesireHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := parseDesireID(r.PathValue("id"), r.PathValue("partition"))
	if err != nil {
		handleError(w, r, h.log, err)
		return
	}
	version, err := decodeDeleteVersion(r.URL.Query())
	if err != nil {
		handleError(w, r, h.log, err)
		return
	}
	caller, _ := auth.CallerFromContext(r.Context())
	if err := h.service.Delete(r.Context(), caller, id, version); err != nil {
		handleError(w, r, h.log, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
