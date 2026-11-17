package handlers

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/openshift-hyperfleet/hyperfleet-desire-store/internal/auth"
	"github.com/openshift-hyperfleet/hyperfleet-desire-store/internal/errors"
)

const (
	traceparent        = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	invalidCredentials = "invalid-credentials"
)

func TestProblemCarriesTheErrorModelFields(t *testing.T) {
	a := newTestAPI(t)
	res := a.do(t, http.MethodGet, desirePath(applyID), a.adapter(t), "")
	res.expect(t, http.StatusNotFound, errors.CodeNotFound)

	problem := res.problem(t)
	if problem.Type != "https://api.hyperfleet.io/errors/resource-not-found" || problem.Title != "Resource Not Found" {
		t.Fatalf("type/title = %q/%q", problem.Type, problem.Title)
	}
	if problem.Status != http.StatusNotFound || problem.Instance != desirePath(applyID) || problem.Detail == "" {
		t.Fatalf("status/instance/detail = %d/%q/%q", problem.Status, problem.Instance, problem.Detail)
	}
	stamp, err := time.Parse(time.RFC3339, problem.Timestamp)
	if err != nil || time.Since(stamp) > time.Minute || stamp.Location() != time.UTC {
		t.Fatalf("timestamp = %q, want a current RFC 3339 UTC time (%v)", problem.Timestamp, err)
	}
	if problem.TraceID != "" {
		t.Fatalf("trace_id = %q without a traceparent, want it absent", problem.TraceID)
	}
	if problem.Errors != nil || problem.ExpectedVersion != nil || problem.ActualVersion != nil {
		t.Fatalf("a 404 carries validation or version fields: %s", res.body)
	}
}

func TestProblemCarriesTheTraceID(t *testing.T) {
	a := newTestAPI(t)
	res := a.do(t, http.MethodGet, desirePath(applyID), a.adapter(t), "", [2]string{"traceparent", traceparent})
	if got := res.problem(t).TraceID; got != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Fatalf("trace_id = %q, want the traceparent trace ID", got)
	}

	unauthenticated := a.do(t, http.MethodGet, desirePath(applyID), "", "", [2]string{"traceparent", traceparent})
	if got := unauthenticated.problem(t).TraceID; got != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Fatalf("401 trace_id = %q, want the traceparent trace ID", got)
	}
}

func TestValidationProblemCarriesErrors(t *testing.T) {
	a := newTestAPI(t)
	res := a.do(t, http.MethodPut, desirePath(applyID), a.adapter(t), `{"kubeContent":{}}`)
	res.expect(t, http.StatusBadRequest, errors.CodeRequired)
	problem := res.problem(t)
	if problem.Type != errors.TypeBase+"validation-error" || len(problem.Errors) != 1 {
		t.Fatalf("problem = %+v, want a validation-error with one errors entry", problem)
	}
	entry := problem.Errors[0]
	if entry.Field != fieldResourceID || entry.Constraint != errors.ConstraintRequired || entry.Message == "" {
		t.Fatalf("errors[0] = %+v, want resource_id/required with a message", entry)
	}
}

func TestUnauthenticatedProblems(t *testing.T) {
	a := newTestAPI(t)
	// One case per problem type; the auth package covers each rejection cause.
	tests := map[string]struct{ token, code, kind string }{
		"missing":   {"", auth.CodeMissing, "authentication-required"},
		"malformed": {"abc", auth.CodeMalformed, invalidCredentials},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			res := a.do(t, http.MethodGet, listPath(), tc.token, "")
			res.expect(t, http.StatusUnauthorized, tc.code)
			if got := res.problem(t).Type; got != errors.TypeBase+tc.kind {
				t.Fatalf("type = %q, want %s", got, tc.kind)
			}
		})
	}
}

func TestUnknownRoutesAndMethods(t *testing.T) {
	a := newTestAPI(t)
	adapter := a.adapter(t)

	unknown := []string{"/", "/api/hyperfleet/desire-store/v2/partitions/mc-01/desires", desirePath(applyID) + "/x"}
	for _, path := range unknown {
		res := a.do(t, http.MethodGet, path, adapter, "")
		res.expect(t, http.StatusNotFound, errors.CodeEndpointNotFound)
		if res.contractChecked {
			t.Fatalf("%s matched a specification operation", path)
		}
	}
	a.do(t, http.MethodGet, "/no/such/path", "", "").expect(t, http.StatusUnauthorized, auth.CodeMissing)

	// ServeMux serves HEAD wherever GET is routed, so Allow lists it too.
	tests := map[string]struct{ method, path, allow string }{
		"collection":   {http.MethodPost, listPath(), "GET, HEAD"},
		"desire":       {http.MethodPatch, desirePath(applyID), "DELETE, GET, HEAD, PUT"},
		"status route": {http.MethodGet, statusPath(applyID), "PUT"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			res := a.do(t, tc.method, tc.path, adapter, "")
			res.expect(t, http.StatusMethodNotAllowed, "")
			if got := res.header.Get("Allow"); got != tc.allow {
				t.Fatalf("Allow = %q, want %q", got, tc.allow)
			}
			if problem := res.problem(t); problem.Status != http.StatusMethodNotAllowed {
				t.Fatalf("problem status = %d, want 405", problem.Status)
			}
		})
	}
}

func TestWriteProblemHasNoTraceWithoutContext(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/x", nil)
	handleError(recorder, request, slog.New(slog.NewTextHandler(io.Discard, nil)),
		errors.Unavailable(errors.CodeUnavailable, "busy", nil))

	if recorder.Code != http.StatusServiceUnavailable || recorder.Header().Get("Content-Type") != problemContentType {
		t.Fatalf("status/content type = %d/%q", recorder.Code, recorder.Header().Get("Content-Type"))
	}
	var problem errors.Problem
	if err := json.Unmarshal(recorder.Body.Bytes(), &problem); err != nil {
		t.Fatalf("decoding the problem: %v", err)
	}
	if problem.TraceID != "" || problem.Code != errors.CodeUnavailable {
		t.Fatalf("trace_id/code = %q/%q, want no trace_id and %s", problem.TraceID, problem.Code, errors.CodeUnavailable)
	}
}

func TestClientErrorsAreLogged(t *testing.T) {
	var logs strings.Builder
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPut, "/x", nil)
	handleError(recorder, request, slog.New(slog.NewTextHandler(&logs, nil)),
		errors.Invalid(errors.CodeRequired, fieldResourceID, errors.ConstraintRequired, "resource_id is required"))

	for _, want := range []string{"level=INFO", "request rejected", "error_code=" + errors.CodeRequired, "method=PUT"} {
		if !strings.Contains(logs.String(), want) {
			t.Fatalf("log %q does not contain %q", logs.String(), want)
		}
	}
}
