package handlers

import (
	"context"
	stderrors "errors"
	"net/http"
	"sync"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/legacy"
	dsschemas "github.com/openshift-hyperfleet/hyperfleet-desire-store-spec/schemas"
)

var (
	contractOnce   sync.Once
	contractRouter routers.Router
	contractErr    error
)

// loadContract parses the REST v1 specification that the spec module embeds.
func loadContract(t *testing.T) routers.Router {
	t.Helper()
	contractOnce.Do(func() {
		data, err := dsschemas.FS.ReadFile("desire-store/openapi.yaml")
		if err != nil {
			contractErr = err
			return
		}
		doc, err := openapi3.NewLoader().LoadFromData(data)
		if err != nil {
			contractErr = err
			return
		}
		contractRouter, contractErr = legacy.NewRouter(doc)
	})
	if contractErr != nil {
		t.Fatalf("loading the REST v1 specification: %v", contractErr)
	}
	return contractRouter
}

// checkContract validates one response against the operation the request
// matches in the specification. It reports false when no operation matches,
// which is the case for 404 and 405 responses on paths or methods the
// specification does not define.
func checkContract(t *testing.T, request *http.Request, status int, header http.Header, body []byte) bool {
	t.Helper()
	route, pathParams, err := loadContract(t).FindRoute(request)
	if err != nil {
		var routeErr *routers.RouteError
		if stderrors.As(err, &routeErr) {
			return false
		}
		t.Fatalf("finding the specification route for %s %s: %v", request.Method, request.URL, err)
	}

	input := &openapi3filter.ResponseValidationInput{
		RequestValidationInput: &openapi3filter.RequestValidationInput{
			Request:    request,
			PathParams: pathParams,
			Route:      route,
		},
		Status:  status,
		Header:  header,
		Options: &openapi3filter.Options{IncludeResponseStatus: true, MultiError: true},
	}
	input.SetBodyBytes(body)
	if err := openapi3filter.ValidateResponse(context.Background(), input); err != nil {
		t.Errorf("%s %s: the %d response violates the REST v1 specification: %v\nbody: %s",
			request.Method, request.URL, status, err, body)
	}
	return true
}

// TestEveryOperationMatchesTheContract drives each operation through its
// documented responses; the harness validates every one of them.
func TestEveryOperationMatchesTheContract(t *testing.T) {
	a := newTestAPI(t)
	adapter := a.adapter(t)
	applier := a.applier(t)
	id := "apply:apps:deployments:kube-system:coredns"

	steps := []struct {
		method, path, token, body string
		status                    int
	}{
		{http.MethodPut, desirePath(id), adapter, applyBody("coredns"), http.StatusCreated},
		{http.MethodPut, desirePath(id), adapter, applyBody("coredns"), http.StatusConflict},
		{http.MethodPut, desirePath(id), adapter, withVersion(applyBody("coredns"), 1), http.StatusOK},
		{http.MethodGet, desirePath(id), adapter, "", http.StatusOK},
		{http.MethodGet, listPath() + "?limit=1", adapter, "", http.StatusOK},
		{http.MethodPut, statusPath(id), applier, appliedStatus(2), http.StatusNoContent},
		{http.MethodPut, statusPath(id), applier, appliedStatus(2), http.StatusConflict},
		{http.MethodPut, statusPath(id), adapter, appliedStatus(3), http.StatusForbidden},
		{http.MethodPut, desirePath(id), adapter, minimalWriteBody, http.StatusBadRequest},
		{http.MethodPut, desirePath(id), adapter, applyBody("other"), http.StatusUnprocessableEntity},
		{http.MethodGet, listPath() + "?limit=0", adapter, "", http.StatusBadRequest},
		{http.MethodGet, desirePath(id), "", "", http.StatusUnauthorized},
		{http.MethodDelete, desirePath(id) + "?version=3", adapter, "", http.StatusNoContent},
		{http.MethodDelete, desirePath(id) + "?version=3", adapter, "", http.StatusNotFound},
		{http.MethodGet, desirePath(id), adapter, "", http.StatusNotFound},
	}
	for _, step := range steps {
		res := a.do(t, step.method, step.path, step.token, step.body)
		if res.status != step.status {
			t.Fatalf("%s %s: status = %d, want %d; body %s", step.method, step.path, res.status, step.status, res.body)
		}
		if !res.contractChecked {
			t.Fatalf("%s %s: no specification operation matched", step.method, step.path)
		}
	}
}
