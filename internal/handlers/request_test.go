package handlers

import (
	"net/http"
	"strings"
	"testing"

	"github.com/openshift-hyperfleet/hyperfleet-applier/pkg/desire"

	"github.com/openshift-hyperfleet/hyperfleet-desire-store/internal/errors"
	"github.com/openshift-hyperfleet/hyperfleet-desire-store/internal/services"
)

func assertField(t *testing.T, problem errors.Problem, field string) {
	t.Helper()
	if len(problem.Errors) != 1 || problem.Errors[0].Field != field || problem.Errors[0].Message == "" {
		t.Fatalf("errors = %+v, want one entry for field %q", problem.Errors, field)
	}
	assertNoInternals(t, problem)
}

// internalMarkers are fragments of Go and store error text that must not reach
// a client: decoder messages, Go type names, and store field names.
var internalMarkers = []string{"json:", "Go ", "desire.", "v1.", "managementCluster", "DesireType", "ApplySpec"}

// assertNoInternals checks that a problem's detail and messages carry no
// internal error text.
func assertNoInternals(t *testing.T, problem errors.Problem) {
	t.Helper()
	texts := []string{problem.Detail}
	for _, detail := range problem.Errors {
		texts = append(texts, detail.Message)
	}
	for _, text := range texts {
		for _, marker := range internalMarkers {
			if strings.Contains(text, marker) {
				t.Fatalf("problem text %q exposes internal detail %q", text, marker)
			}
		}
	}
}

func TestParseDesireID(t *testing.T) {
	id, problem := parseDesireID("apply:apps:deployments:kube-system:coredns", testPartition)
	if problem != nil {
		t.Fatalf("parseDesireID() problem: %+v", problem)
	}
	want := desire.Identity{
		ManagementCluster: testPartition, Type: desire.TypeApply, Group: "apps",
		Resource: "deployments", Namespace: "kube-system", Name: "coredns",
	}
	if id != want {
		t.Fatalf("parseDesireID() = %+v, want %+v", id, want)
	}

	clusterScoped, problem := parseDesireID("apply:rbac.authorization.k8s.io:clusterroles::admin", testPartition)
	if problem != nil || clusterScoped.Namespace != "" || clusterScoped.Name != "admin" {
		t.Fatalf("cluster-scoped identifier = %+v, %+v", clusterScoped, problem)
	}
	if got := services.FormatDesireID(clusterScoped); got != "apply:rbac.authorization.k8s.io:clusterroles::admin" {
		t.Fatalf("services.FormatDesireID() = %q, want the original identifier", got)
	}
}

func TestInvalidIdentifiers(t *testing.T) {
	a := newTestAPI(t)
	for _, id := range []string{
		"apply:apps",
		"patch:apps:deployments:default:x",
		"apply:apps::default:x",
		"apply:apps:deployments:default:",
		"apply:apps:deployments:default:..",
		// The specification allows colons in names, but the shared store's
		// identity validation rejects them, so the service answers 400.
		"apply:rbac.authorization.k8s.io:clusterroles::system:controller:x",
	} {
		t.Run(id, func(t *testing.T) {
			res := a.do(t, http.MethodGet, desirePath(id), a.adapter(t), "")
			res.expect(t, http.StatusBadRequest, errors.CodeInvalidPath)
			assertField(t, res.problem(t), fieldDesireID)
		})
	}
}

func TestInvalidWriteBodies(t *testing.T) {
	const otherRead = "read::configmaps:default:x"
	const otherDelete = "delete::configmaps:default:x"
	tests := []struct {
		name, id, body string
		code, field    string
	}{
		{"invalid JSON", applyID, `{"resource_id":`, errors.CodeInvalidBody, fieldBody},
		{"array body", applyID, `[]`, errors.CodeInvalidBody, fieldBody},
		{"trailing data", applyID, applyBody("coredns") + `{}`, errors.CodeInvalidBody, fieldBody},
		{"missing resource_id", otherRead, `{"target_version":"v1"}`, errors.CodeRequired, fieldResourceID},
		{"null owner on apply", applyID, `{"owner":null,` + applyBody("coredns")[1:], errors.CodeInvalidField, fieldOwner},
		{"empty resource_id", otherRead, `{"resource_id":"","target_version":"v1"}`,
			errors.CodeInvalidField, fieldResourceID},
		{"owner in body", otherRead, `{"resource_id":"c","target_version":"v1","owner":"me"}`,
			errors.CodeInvalidField, fieldOwner},
		{"apply without kubeContent", applyID, `{"resource_id":"c"}`, errors.CodeRequired, fieldKubeContent},
		{"apply with string kubeContent", applyID, `{"resource_id":"c","kubeContent":"x"}`,
			errors.CodeInvalidField, fieldKubeContent},
		{"read without target_version", otherRead, `{"resource_id":"c"}`, errors.CodeRequired, fieldTargetVersion},
		{"kubeContent on delete", otherDelete, `{"resource_id":"c","kubeContent":{}}`,
			errors.CodeInvalidField, fieldKubeContent},
		{"null kubeContent on delete", otherDelete, `{"resource_id":"c","kubeContent":null}`,
			errors.CodeInvalidField, fieldKubeContent},
		{"null kubeContent on read", otherRead, `{"resource_id":"c","target_version":"v1","kubeContent":null}`,
			errors.CodeInvalidField, fieldKubeContent},
		{"version on delete", otherDelete, `{"resource_id":"c","version":1}`, errors.CodeInvalidField, fieldVersion},
		{"negative version", applyID, withVersion(applyBody("coredns"), -1), errors.CodeInvalidField, fieldVersion},
		{"string version", otherDelete, `{"resource_id":"c","version":"0"}`, errors.CodeInvalidField, fieldVersion},
		{"null version", applyID, `{"version":null,` + applyBody("coredns")[1:], errors.CodeInvalidField, fieldVersion},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a := newTestAPI(t)
			res := a.do(t, http.MethodPut, desirePath(tc.id), a.adapter(t), tc.body)
			res.expect(t, http.StatusBadRequest, tc.code)
			assertField(t, res.problem(t), tc.field)
			a.do(t, http.MethodGet, desirePath(tc.id), a.adapter(t), "").expect(t, http.StatusNotFound, errors.CodeNotFound)
		})
	}
}

// TestUndeclaredMembersAreIgnored checks that a newer client's optional
// additive field does not fail a write or a status write.
func TestUndeclaredMembersAreIgnored(t *testing.T) {
	a := newTestAPI(t)
	a.do(t, http.MethodPut, desirePath(readID), a.adapter(t), `{"foo":1,`+readBody()[1:]).
		expect(t, http.StatusCreated, "")
	a.do(t, http.MethodPut, desirePath(applyID), a.adapter(t), applyBody("coredns")).expect(t, http.StatusCreated, "")
	a.do(t, http.MethodPut, statusPath(applyID), a.applier(t), `{"foo":1,`+appliedStatus(1)[1:]).
		expect(t, http.StatusNoContent, "")
}

func TestBodyAtTheLimitIsAccepted(t *testing.T) {
	a := newTestAPI(t)
	const frame = `{"resource_id":""}`
	body := `{"resource_id":"` + strings.Repeat("x", maxBodyBytes-len(frame)) + `"}`
	if len(body) != maxBodyBytes {
		t.Fatalf("body is %d bytes, want %d", len(body), maxBodyBytes)
	}

	a.do(t, http.MethodPut, desirePath(deleteID), a.adapter(t), body).expect(t, http.StatusCreated, "")
}

func TestBodyOverTheLimitIsRejected(t *testing.T) {
	a := newTestAPI(t)
	body := `{"resource_id":"` + strings.Repeat("x", maxBodyBytes) + `"}`

	res := a.do(t, http.MethodPut, desirePath(deleteID), a.adapter(t), body)
	res.expect(t, http.StatusBadRequest, errors.CodeInvalidBody)
	assertField(t, res.problem(t), fieldBody)
}

func TestKubeContentMustMatchThePath(t *testing.T) {
	tests := map[string]struct{ body, field string }{
		"name": {applyBody("other"), fieldManifestName},
		"namespace": {`{"resource_id":"c","kubeContent":{"apiVersion":"apps/v1","kind":"Deployment",` +
			`"metadata":{"name":"coredns","namespace":"default"}}}`, fieldManifestNamespace},
		"group": {`{"resource_id":"c","kubeContent":{"apiVersion":"v1","kind":"Deployment",` +
			`"metadata":{"name":"coredns","namespace":"kube-system"}}}`, fieldManifestAPIVersion},
		"case variant of metadata": {`{"resource_id":"c","kubeContent":{"apiVersion":"apps/v1",` +
			`"kind":"Deployment","metadata":{"name":"other","namespace":"default"},` +
			`"Metadata":{"name":"coredns","namespace":"kube-system"}}}`, fieldManifestNamespace},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			a := newTestAPI(t)
			res := a.do(t, http.MethodPut, desirePath(applyID), a.adapter(t), tc.body)
			res.expect(t, http.StatusUnprocessableEntity, errors.CodeInvalidField)
			assertField(t, res.problem(t), tc.field)
		})
	}
}

func TestInvalidStatusBodies(t *testing.T) {
	tests := []struct {
		name, id, body string
		code, field    string
	}{
		{"missing status", applyID, `{"version":1}`, errors.CodeRequired, fieldStatus},
		{"string status", readID, `{"status":"done"}`, errors.CodeInvalidField, fieldStatus},
		{"malformed conditions", applyID, `{"version":1,"status":{"conditions":"x"}}`, errors.CodeInvalidField, fieldStatus},
		{"malformed condition time", applyID, `{"version":1,"status":{"conditions":[{"lastTransitionTime":"x"}]}}`,
			errors.CodeInvalidField, fieldStatus},
		{"malformed read conditions", readID, `{"status":{"conditions":"x"}}`, errors.CodeInvalidField, fieldStatus},
		{"missing version", applyID, `{"status":{}}`, errors.CodeRequired, fieldVersion},
		{"zero version", applyID, `{"version":0,"status":{}}`, errors.CodeInvalidField, fieldVersion},
		{"null version", applyID, `{"version":null,"status":{}}`, errors.CodeInvalidField, fieldVersion},
		{"invalid JSON", applyID, `{"status":`, errors.CodeInvalidBody, fieldBody},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a := newTestAPI(t)
			a.do(t, http.MethodPut, desirePath(applyID), a.adapter(t), applyBody("coredns")).
				expect(t, http.StatusCreated, "")
			a.do(t, http.MethodPut, desirePath(readID), a.adapter(t), readBody()).expect(t, http.StatusCreated, "")
			before := a.do(t, http.MethodGet, desirePath(tc.id), a.adapter(t), "")

			res := a.do(t, http.MethodPut, statusPath(tc.id), a.applier(t), tc.body)
			res.expect(t, http.StatusBadRequest, tc.code)
			assertField(t, res.problem(t), tc.field)

			after := a.do(t, http.MethodGet, desirePath(tc.id), a.adapter(t), "")
			if string(after.body) != string(before.body) {
				t.Fatalf("a rejected status write changed the desire:\nbefore %s\nafter  %s", before.body, after.body)
			}
		})
	}
}

func TestInvalidDeleteVersions(t *testing.T) {
	a := newTestAPI(t)
	tests := map[string]struct{ query, code string }{
		"missing":     {"", errors.CodeRequired},
		"non-integer": {"?version=abc", errors.CodeInvalidQuery},
		"zero":        {"?version=0", errors.CodeInvalidQuery},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			res := a.do(t, http.MethodDelete, desirePath(applyID)+tc.query, a.adapter(t), "")
			res.expect(t, http.StatusBadRequest, tc.code)
			assertField(t, res.problem(t), fieldVersion)
		})
	}
}

// TestRequestsAreCheckedAgainstTheContract covers the contract rules that
// the validator in front of the handlers enforces and no handler repeats.
func TestRequestsAreCheckedAgainstTheContract(t *testing.T) {
	a := newTestAPI(t)
	adapter := a.adapter(t)

	res := a.do(t, http.MethodPut, desirePath(deleteID), adapter, deleteBody(), [2]string{"Content-Type", "text/plain"})
	res.expect(t, http.StatusBadRequest, errors.CodeInvalidBody)
	assertField(t, res.problem(t), fieldBody)

	// StatusWrite.version has minimum 1 and is not nullable for every
	// desire type, so a ReadDesire status write that sends it is checked too.
	a.do(t, http.MethodPut, desirePath(readID), adapter, readBody()).expect(t, http.StatusCreated, "")
	for _, body := range []string{`{"version":null,"status":{}}`, `{"version":0,"status":{}}`} {
		res := a.do(t, http.MethodPut, statusPath(readID), a.applier(t), body)
		res.expect(t, http.StatusBadRequest, errors.CodeInvalidField)
		assertField(t, res.problem(t), fieldVersion)
	}
}
