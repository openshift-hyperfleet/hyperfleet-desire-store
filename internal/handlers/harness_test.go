package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/openshift-hyperfleet/hyperfleet-applier/pkg/desire/store/memory"

	"github.com/openshift-hyperfleet/hyperfleet-desire-store/internal/auth"
	"github.com/openshift-hyperfleet/hyperfleet-desire-store/internal/auth/authtest"
	"github.com/openshift-hyperfleet/hyperfleet-desire-store/internal/errors"
	"github.com/openshift-hyperfleet/hyperfleet-desire-store/internal/services"
)

const (
	testPartition = "mc-01"
	adapterA      = "system:serviceaccount:hyperfleet:cluster-adapter-hyperfleet-adapter"
	adapterB      = "system:serviceaccount:hyperfleet:nodepool-adapter-hyperfleet-adapter"
	applierID     = "system:serviceaccount:hyperfleet:applier"

	// minimalWriteBody carries only the one property every write requires.
	minimalWriteBody = `{"resource_id":"c"}`
)

type testAPI struct {
	signer  *authtest.Signer
	store   services.Store
	handler http.Handler
}

type apiOptions struct {
	store services.Store
}

// newTestAPI serves the API over the applier memory store, or over the given
// store, with Wristbands from a test signer.
func newTestAPI(t *testing.T, opts ...func(*apiOptions)) *testAPI {
	t.Helper()
	options := apiOptions{store: memory.New()}
	for _, opt := range opts {
		opt(&options)
	}

	signer := authtest.NewSigner(t)
	validator, err := auth.NewValidator(context.Background(), signer.Options())
	if err != nil {
		t.Fatalf("NewValidator() error: %v", err)
	}
	t.Cleanup(validator.Close)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return &testAPI{
		signer:  signer,
		store:   options.store,
		handler: NewRouter(services.NewDesireService(options.store, authtest.SweeperID), validator, logger),
	}
}

func (a *testAPI) token(t *testing.T, class auth.CallerClass, identity, partition string) string {
	t.Helper()
	return a.signer.Sign(t, authtest.Claims(string(class), identity, partition))
}

func (a *testAPI) adapter(t *testing.T) string {
	return a.token(t, auth.ClassFleetScoped, adapterA, testPartition)
}

func (a *testAPI) otherAdapter(t *testing.T) string {
	return a.token(t, auth.ClassFleetScoped, adapterB, testPartition)
}

func (a *testAPI) applier(t *testing.T) string {
	return a.token(t, auth.ClassRemoteApplier, applierID, testPartition)
}

func (a *testAPI) sweeper(t *testing.T) string {
	return a.token(t, auth.ClassFleetScoped, authtest.SweeperID, testPartition)
}

type result struct {
	status          int
	header          http.Header
	body            []byte
	contractChecked bool
}

// do sends one request, with the Wristband when token is set, and validates
// the response against the REST v1 specification.
func (a *testAPI) do(t *testing.T, method, path, token, body string, headers ...[2]string) result {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	request := httptest.NewRequest(method, path, reader)
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		request.Header.Set(auth.HeaderWristband, token)
	}
	for _, header := range headers {
		request.Header.Set(header[0], header[1])
	}

	recorder := httptest.NewRecorder()
	a.handler.ServeHTTP(recorder, request)
	response := recorder.Result()
	defer func() {
		if err := response.Body.Close(); err != nil {
			t.Errorf("closing the response body: %v", err)
		}
	}()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("reading the response: %v", err)
	}

	res := result{status: response.StatusCode, header: response.Header, body: data}
	res.contractChecked = checkContract(t, httptest.NewRequest(method, path, nil), res.status, res.header, data)
	return res
}

func (r result) problem(t *testing.T) errors.Problem {
	t.Helper()
	if got := r.header.Get("Content-Type"); got != problemContentType {
		t.Fatalf("Content-Type = %q, want %q; body %s", got, problemContentType, r.body)
	}
	var problem errors.Problem
	if err := json.Unmarshal(r.body, &problem); err != nil {
		t.Fatalf("decoding the problem %s: %v", r.body, err)
	}
	return problem
}

func (r result) object(t *testing.T) map[string]any {
	t.Helper()
	var object map[string]any
	decoder := json.NewDecoder(strings.NewReader(string(r.body)))
	decoder.UseNumber()
	if err := decoder.Decode(&object); err != nil {
		t.Fatalf("decoding %s: %v", r.body, err)
	}
	return object
}

// expect fails the test unless the response has the status and, for an
// error, the problem code.
func (r result) expect(t *testing.T, status int, code string) {
	t.Helper()
	if r.status != status {
		t.Fatalf("status = %d, want %d; body %s", r.status, status, r.body)
	}
	if code != "" {
		if got := r.problem(t).Code; got != code {
			t.Fatalf("code = %q, want %q; body %s", got, code, r.body)
		}
	}
}

func listPath() string {
	return "/api/hyperfleet/desire-store/v1/partitions/" + testPartition + "/desires"
}

func partitionDesirePath(partition, id string) string {
	return "/api/hyperfleet/desire-store/v1/partitions/" + partition + "/desires/" + id
}

func desirePath(id string) string { return partitionDesirePath(testPartition, id) }

func statusPath(id string) string { return desirePath(id) + "/status" }

func applyBody(name string) string {
	return fmt.Sprintf(`{"resource_id":"clusters/cls-123","kubeContent":{"apiVersion":"apps/v1",`+
		`"kind":"Deployment","metadata":{"name":%q,"namespace":"kube-system"},"spec":{"replicas":2}}}`, name)
}

func readBody() string {
	return `{"resource_id":"clusters/cls-123","target_version":"v1"}`
}

func deleteBody() string {
	return `{"resource_id":"clusters/cls-123"}`
}

// withVersion adds a version member to a JSON object body.
func withVersion(body string, version int64) string {
	return fmt.Sprintf(`{"version":%d,%s`, version, strings.TrimPrefix(body, "{"))
}

func appliedStatus(version int64) string {
	return fmt.Sprintf(`{"version":%d,"status":{"conditions":[{"type":"Successful","status":"True",`+
		`"reason":"Applied","message":"applied","lastTransitionTime":"2026-11-12T10:00:00Z","observedGeneration":1}]}}`,
		version)
}

func deletedStatus(version int64) string {
	return fmt.Sprintf(`{"version":%d,"status":{"conditions":[{"type":"Successful","status":"True",`+
		`"reason":"Deleted","message":"deleted","lastTransitionTime":"2026-11-12T10:00:00Z"}]}}`, version)
}

func numberField(t *testing.T, object map[string]any, field string) int64 {
	t.Helper()
	number, ok := object[field].(json.Number)
	if !ok {
		t.Fatalf("%s = %v, want a number", field, object[field])
	}
	value, err := number.Int64()
	if err != nil {
		t.Fatalf("%s = %v: %v", field, number, err)
	}
	return value
}
