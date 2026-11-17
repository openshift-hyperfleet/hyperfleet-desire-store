package handlers

import (
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/openshift-hyperfleet/hyperfleet-desire-store/internal/auth/authtest"
	"github.com/openshift-hyperfleet/hyperfleet-desire-store/internal/errors"
	"github.com/openshift-hyperfleet/hyperfleet-desire-store/internal/services"
)

const (
	applyID  = "apply:apps:deployments:kube-system:coredns"
	readID   = "read:apps:deployments:kube-system:coredns"
	deleteID = "delete:apps:deployments:kube-system:coredns"
)

func keys(object map[string]any) []string {
	return slices.Sorted(maps.Keys(object))
}

func listItems(t *testing.T, list map[string]any) []any {
	t.Helper()
	items, ok := list["items"].([]any)
	if !ok {
		t.Fatalf("items = %v, want an array", list["items"])
	}
	return items
}

func TestWriteAndGetEachType(t *testing.T) {
	a := newTestAPI(t)
	adapter := a.adapter(t)

	created := a.do(t, http.MethodPut, desirePath(applyID), adapter, applyBody("coredns"))
	created.expect(t, http.StatusCreated, "")
	a.do(t, http.MethodPut, desirePath(readID), adapter, readBody()).expect(t, http.StatusCreated, "")
	otherDelete := "delete::configmaps:default:settings"
	a.do(t, http.MethodPut, desirePath(otherDelete), adapter, deleteBody()).expect(t, http.StatusCreated, "")

	apply := a.do(t, http.MethodGet, desirePath(applyID), adapter, "")
	apply.expect(t, http.StatusOK, "")
	if string(apply.body) != string(created.body) {
		t.Fatalf("GET after create = %s, want the create response %s", apply.body, created.body)
	}
	object := apply.object(t)
	want := map[string]any{
		"id": applyID, fieldType: "apply", "group": "apps", "resource": "deployments",
		"namespace": "kube-system", "name": "coredns", fieldOwner: adapterA, fieldResourceID: "clusters/cls-123",
	}
	for field, value := range want {
		if object[field] != value {
			t.Fatalf("apply %s = %v, want %v", field, object[field], value)
		}
	}
	assertVersionGeneration(t, object, 1, 1)
	if _, ok := object[fieldKubeContent]; !ok {
		t.Fatal("an ApplyDesire must carry kubeContent")
	}
	for _, absent := range []string{fieldTargetVersion, fieldStatus} {
		if _, ok := object[absent]; ok {
			t.Fatalf("an ApplyDesire without status must not carry %s: %s", absent, apply.body)
		}
	}

	read := a.do(t, http.MethodGet, desirePath(readID), adapter, "")
	read.expect(t, http.StatusOK, "")
	readObject := read.object(t)
	if readObject[fieldTargetVersion] != "v1" {
		t.Fatalf("read target_version = %v, want v1", readObject[fieldTargetVersion])
	}
	if _, ok := readObject[fieldKubeContent]; ok {
		t.Fatalf("a ReadDesire must not carry top-level kubeContent: %s", read.body)
	}

	deleted := a.do(t, http.MethodGet, desirePath(otherDelete), adapter, "")
	deleted.expect(t, http.StatusOK, "")
	for _, absent := range []string{fieldKubeContent, fieldTargetVersion} {
		if _, ok := deleted.object(t)[absent]; ok {
			t.Fatalf("a DeleteDesire must not carry %s: %s", absent, deleted.body)
		}
	}
}

func TestKubeContentKeepsLargeIntegers(t *testing.T) {
	a := newTestAPI(t)
	body := strings.Replace(applyBody("coredns"), `"replicas":2`, `"replicas":9007199254740993`, 1)

	a.do(t, http.MethodPut, desirePath(applyID), a.adapter(t), body).expect(t, http.StatusCreated, "")

	got := a.do(t, http.MethodGet, desirePath(applyID), a.adapter(t), "")
	if !strings.Contains(string(got.body), `"replicas":9007199254740993`) {
		t.Fatalf("kubeContent lost integer precision: %s", got.body)
	}
}

func TestApplyUpdate(t *testing.T) {
	a := newTestAPI(t)
	adapter := a.adapter(t)
	a.do(t, http.MethodPut, desirePath(applyID), adapter, applyBody("coredns")).expect(t, http.StatusCreated, "")

	changed := strings.Replace(applyBody("coredns"), `"replicas":2`, `"replicas":3`, 1)
	updated := a.do(t, http.MethodPut, desirePath(applyID), adapter, withVersion(changed, 1))
	updated.expect(t, http.StatusOK, "")
	object := updated.object(t)
	assertVersionGeneration(t, object, 2, 2)

	absent := "apply::configmaps:default:missing"
	a.do(t, http.MethodPut, desirePath(absent), adapter,
		withVersion(`{"resource_id":"c","kubeContent":{"apiVersion":"v1","kind":"ConfigMap",`+
			`"metadata":{"name":"missing","namespace":"default"}}}`, 1)).
		expect(t, http.StatusNotFound, errors.CodeNotFound)
}

func TestStatusWrites(t *testing.T) {
	a := newTestAPI(t)
	adapter, applier := a.adapter(t), a.applier(t)
	a.do(t, http.MethodPut, desirePath(applyID), adapter, applyBody("coredns")).expect(t, http.StatusCreated, "")
	a.do(t, http.MethodPut, desirePath(readID), adapter, readBody()).expect(t, http.StatusCreated, "")
	otherDelete := "delete::configmaps:default:settings"
	a.do(t, http.MethodPut, desirePath(otherDelete), adapter, deleteBody()).expect(t, http.StatusCreated, "")

	res := a.do(t, http.MethodPut, statusPath(applyID), applier, appliedStatus(1))
	res.expect(t, http.StatusNoContent, "")
	if len(res.body) != 0 {
		t.Fatalf("a status write returned a body: %s", res.body)
	}
	apply := a.do(t, http.MethodGet, desirePath(applyID), adapter, "").object(t)
	assertVersionGeneration(t, apply, 2, 1)
	if !strings.Contains(fmt.Sprint(apply[fieldStatus]), "Applied") {
		t.Fatalf("status = %v, want the Applied condition", apply[fieldStatus])
	}

	a.do(t, http.MethodPut, statusPath(otherDelete), applier, deletedStatus(1)).expect(t, http.StatusNoContent, "")

	readStatus := `{"status":{"kubeContent":{"apiVersion":"apps/v1","kind":"Deployment"}}}`
	a.do(t, http.MethodPut, statusPath(readID), applier, readStatus).expect(t, http.StatusNoContent, "")
	read := a.do(t, http.MethodGet, desirePath(readID), adapter, "")
	readObject := read.object(t)
	if numberField(t, readObject, fieldVersion) != 1 {
		t.Fatalf("a ReadDesire status write changed the version to %v", readObject[fieldVersion])
	}
	if !strings.Contains(string(read.body), `"status":{"kubeContent":{"apiVersion":"apps/v1"`) {
		t.Fatalf("read status.kubeContent missing: %s", read.body)
	}
}

func TestDeleteDesire(t *testing.T) {
	a := newTestAPI(t)
	adapter := a.adapter(t)
	a.do(t, http.MethodPut, desirePath(readID), adapter, readBody()).expect(t, http.StatusCreated, "")

	res := a.do(t, http.MethodDelete, desirePath(readID)+"?version=1", adapter, "")
	res.expect(t, http.StatusNoContent, "")
	a.do(t, http.MethodGet, desirePath(readID), adapter, "").expect(t, http.StatusNotFound, errors.CodeNotFound)
	a.do(t, http.MethodDelete, desirePath(readID)+"?version=1", adapter, "").
		expect(t, http.StatusNotFound, errors.CodeNotFound)
}

func TestConflictCodes(t *testing.T) {
	t.Run("CNF-001 on a second create", func(t *testing.T) {
		a := newTestAPI(t)
		a.do(t, http.MethodPut, desirePath(applyID), a.adapter(t), applyBody("coredns")).
			expect(t, http.StatusCreated, "")
		res := a.do(t, http.MethodPut, desirePath(applyID), a.adapter(t), applyBody("coredns"))
		res.expect(t, http.StatusConflict, errors.CodeAlreadyExists)
		if got := res.problem(t).Type; got != errors.TypeBase+"resource-conflict" {
			t.Fatalf("type = %q, want resource-conflict", got)
		}
	})

	t.Run("CNF-002 on a stale update", func(t *testing.T) {
		a := newTestAPI(t)
		adapter := a.adapter(t)
		a.do(t, http.MethodPut, desirePath(applyID), adapter, applyBody("coredns")).expect(t, http.StatusCreated, "")
		a.do(t, http.MethodPut, desirePath(applyID), adapter, withVersion(applyBody("coredns"), 1)).
			expect(t, http.StatusOK, "")

		res := a.do(t, http.MethodPut, desirePath(applyID), adapter, withVersion(applyBody("coredns"), 1))
		res.expect(t, http.StatusConflict, errors.CodeVersionConflict)
		assertVersions(t, res.problem(t), 1, 2)
	})

	t.Run("CNF-002 on a stale status write", func(t *testing.T) {
		a := newTestAPI(t)
		a.do(t, http.MethodPut, desirePath(applyID), a.adapter(t), applyBody("coredns")).
			expect(t, http.StatusCreated, "")
		a.do(t, http.MethodPut, statusPath(applyID), a.applier(t), appliedStatus(1)).
			expect(t, http.StatusNoContent, "")

		res := a.do(t, http.MethodPut, statusPath(applyID), a.applier(t), appliedStatus(1))
		res.expect(t, http.StatusConflict, errors.CodeVersionConflict)
		problem := res.problem(t)
		assertVersions(t, problem, 1, 2)
		if problem.Type != errors.TypeBase+"version-conflict" ||
			problem.Detail != "Desire was modified by another request. Expected version 1, found version 2." {
			t.Fatalf("problem = %+v, want version-conflict with both versions in detail", problem)
		}
	})

	t.Run("CNF-003 while a delete is pending", func(t *testing.T) {
		a := newTestAPI(t)
		a.do(t, http.MethodPut, desirePath(deleteID), a.adapter(t), deleteBody()).expect(t, http.StatusCreated, "")
		a.do(t, http.MethodPut, desirePath(applyID), a.adapter(t), applyBody("coredns")).
			expect(t, http.StatusConflict, errors.CodeDeletePending)
	})

	t.Run("CNF-004 for a second caller identity", func(t *testing.T) {
		a := newTestAPI(t)
		a.do(t, http.MethodPut, desirePath(applyID), a.adapter(t), applyBody("coredns")).
			expect(t, http.StatusCreated, "")
		other := a.otherAdapter(t)

		a.do(t, http.MethodPut, desirePath(readID), other, readBody()).expect(t, http.StatusConflict,
			errors.CodeOwnerConflict)
		a.do(t, http.MethodPut, desirePath(applyID), other, withVersion(applyBody("coredns"), 1)).
			expect(t, http.StatusConflict, errors.CodeOwnerConflict)
		a.do(t, http.MethodDelete, desirePath(applyID)+"?version=1", other, "").
			expect(t, http.StatusConflict, errors.CodeOwnerConflict)
		a.do(t, http.MethodPut, desirePath(deleteID), other, deleteBody()).
			expect(t, http.StatusConflict, errors.CodeOwnerConflict)

		owner := a.do(t, http.MethodGet, desirePath(applyID), a.adapter(t), "").object(t)[fieldOwner]
		if owner != adapterA {
			t.Fatalf("owner after rejected writes = %v, want %s", owner, adapterA)
		}
	})

	t.Run("CNF-002 before CNF-004", func(t *testing.T) {
		a := newTestAPI(t)
		a.do(t, http.MethodPut, desirePath(applyID), a.adapter(t), applyBody("coredns")).
			expect(t, http.StatusCreated, "")
		other := a.otherAdapter(t)

		res := a.do(t, http.MethodDelete, desirePath(applyID)+"?version=5", other, "")
		res.expect(t, http.StatusConflict, errors.CodeVersionConflict)
		assertVersions(t, res.problem(t), 5, 1)
		a.do(t, http.MethodDelete, desirePath(applyID)+"?version=1", other, "").
			expect(t, http.StatusConflict, errors.CodeOwnerConflict)
	})
}

func assertVersions(t *testing.T, problem errors.Problem, expected, actual int64) {
	t.Helper()
	if problem.ExpectedVersion == nil || *problem.ExpectedVersion != expected {
		t.Fatalf("expected_version = %v, want %d", problem.ExpectedVersion, expected)
	}
	if problem.ActualVersion == nil || *problem.ActualVersion != actual {
		t.Fatalf("actual_version = %v, want %d", problem.ActualVersion, actual)
	}
}

func TestStatusWriteToAMissingDesire(t *testing.T) {
	a := newTestAPI(t)
	a.do(t, http.MethodPut, statusPath(applyID), a.applier(t), appliedStatus(1)).
		expect(t, http.StatusNotFound, errors.CodeNotFound)
	a.do(t, http.MethodPut, statusPath(readID), a.applier(t), `{"status":{}}`).
		expect(t, http.StatusNotFound, errors.CodeNotFound)
}

// TestDeleteSupersedesApply walks CAS-8 from the Desire Store test plan.
func TestDeleteSupersedesApply(t *testing.T) {
	a := newTestAPI(t)
	adapter, other, applier := a.adapter(t), a.otherAdapter(t), a.applier(t)

	a.do(t, http.MethodPut, desirePath(applyID), adapter, applyBody("coredns")).expect(t, http.StatusCreated, "")
	a.do(t, http.MethodPut, desirePath(readID), other, readBody()).expect(t, http.StatusConflict, errors.CodeOwnerConflict)
	a.do(t, http.MethodPut, desirePath(readID), adapter, readBody()).expect(t, http.StatusCreated, "")

	a.do(t, http.MethodPut, desirePath(deleteID), adapter, deleteBody()).expect(t, http.StatusCreated, "")
	a.do(t, http.MethodGet, desirePath(applyID), adapter, "").expect(t, http.StatusNotFound, errors.CodeNotFound)
	read := a.do(t, http.MethodGet, desirePath(readID), adapter, "")
	read.expect(t, http.StatusOK, "")
	if owner := read.object(t)[fieldOwner]; owner != adapterA {
		t.Fatalf("read sibling owner = %v, want %s", owner, adapterA)
	}
	a.do(t, http.MethodPut, statusPath(applyID), applier, appliedStatus(1)).expect(t, http.StatusNotFound,
		errors.CodeNotFound)

	a.do(t, http.MethodPut, desirePath(applyID), adapter, applyBody("coredns")).
		expect(t, http.StatusConflict, errors.CodeDeletePending)

	a.do(t, http.MethodPut, statusPath(deleteID), applier, deletedStatus(1)).expect(t, http.StatusNoContent, "")
	a.do(t, http.MethodPut, desirePath(applyID), adapter, applyBody("coredns")).expect(t, http.StatusCreated, "")
	a.do(t, http.MethodGet, desirePath(deleteID), adapter, "").expect(t, http.StatusNotFound, errors.CodeNotFound)
}

func TestSweeper(t *testing.T) {
	a := newTestAPI(t)
	sweeper := a.sweeper(t)

	a.do(t, http.MethodPut, desirePath(applyID), sweeper, applyBody("coredns")).
		expect(t, http.StatusForbidden, errors.CodePermissionDenied)

	orphan := "delete::configmaps:default:orphan"
	created := a.do(t, http.MethodPut, desirePath(orphan), sweeper, deleteBody())
	created.expect(t, http.StatusCreated, "")
	if owner := created.object(t)[fieldOwner]; owner != authtest.SweeperID {
		t.Fatalf("sweeper DeleteDesire owner = %v, want the sweeper identity", owner)
	}
	a.do(t, http.MethodDelete, desirePath(orphan)+"?version=1", sweeper, "").expect(t, http.StatusNoContent, "")

	// The shared store still runs the owner check for the sweeper, so a
	// sweeper DeleteDesire for an adapter-owned object is an owner conflict
	// until the store-interface change lands (the design's open limit).
	a.do(t, http.MethodPut, desirePath(applyID), a.adapter(t), applyBody("coredns")).expect(t, http.StatusCreated, "")
	a.do(t, http.MethodPut, desirePath(deleteID), sweeper, deleteBody()).
		expect(t, http.StatusConflict, errors.CodeOwnerConflict)
}

// typeRead is the wire value of a ReadDesire type.
const typeRead = "read"

// referenceID checks that a list item carries only the identity fields and
// the version, and returns its desire identifier.
func referenceID(t *testing.T, item any) string {
	t.Helper()
	ref, ok := item.(map[string]any)
	if !ok {
		t.Fatalf("list item = %v, want an object", item)
	}
	want := "group,name,namespace,resource,type,version"
	if ref[fieldType] == typeRead {
		want = "group,name,namespace,resource,target_version,type,version"
	}
	if got := strings.Join(keys(ref), ","); got != want {
		t.Fatalf("list item fields = %s, want %s", got, want)
	}
	return fmt.Sprintf("%v:%v:%v:%v:%v", ref[fieldType], ref["group"], ref["resource"], ref["namespace"], ref["name"])
}

func TestListPages(t *testing.T) {
	a := newTestAPI(t)
	adapter := a.adapter(t)
	ids := []string{
		"apply::configmaps:default:a", "apply::configmaps:default:b", "apply::configmaps:other:a",
		"read::configmaps:default:c", "read:apps:deployments:default:d", "delete::secrets:default:e",
		"delete::secrets:default:f",
	}
	for _, id := range ids {
		var body string
		parts := strings.SplitN(id, ":", 5)
		switch parts[0] {
		case "apply":
			body = fmt.Sprintf(`{"resource_id":"c","kubeContent":{"apiVersion":"v1","kind":"ConfigMap",`+
				`"metadata":{"name":%q,"namespace":%q}}}`, parts[4], parts[3])
		case "read":
			body = readBody()
		default:
			body = deleteBody()
		}
		a.do(t, http.MethodPut, desirePath(id), adapter, body).expect(t, http.StatusCreated, "")
	}

	seen := map[string]int{}
	cursor := ""
	var pageSizes []int
	for page := 0; page < 10; page++ {
		path := listPath() + "?limit=3"
		if cursor != "" {
			path += "&cursor=" + cursor
		}
		res := a.do(t, http.MethodGet, path, adapter, "")
		res.expect(t, http.StatusOK, "")
		object := res.object(t)
		items := listItems(t, object)
		pageSizes = append(pageSizes, len(items))
		for _, item := range items {
			seen[referenceID(t, item)]++
		}
		next, ok := object["next_cursor"].(string)
		if !ok {
			break
		}
		cursor = next
	}

	if fmt.Sprint(pageSizes) != "[3 3 1]" {
		t.Fatalf("page sizes = %v, want [3 3 1]", pageSizes)
	}
	for _, id := range ids {
		if seen[id] != 1 {
			t.Fatalf("%s appeared %d times across pages, want once (seen %v)", id, seen[id], seen)
		}
	}

	filtered := a.do(t, http.MethodGet, listPath()+"?type=read", adapter, "").object(t)
	if items := listItems(t, filtered); len(items) != 2 {
		t.Fatalf("type=read returned %d items, want 2", len(items))
	}
	if _, ok := filtered["next_cursor"]; ok {
		t.Fatal("a single page must not carry next_cursor")
	}
}

// The Applier's read poll needs target_version on every pass, and a recreated
// ReadDesire keeps its identity and version 1, so only the reference shows a
// changed target version.
func TestListCarriesReadTargetVersion(t *testing.T) {
	a := newTestAPI(t)
	adapter := a.adapter(t)
	a.do(t, http.MethodPut, desirePath("read::configmaps:default:c"), adapter, readBody()).
		expect(t, http.StatusCreated, "")
	a.do(t, http.MethodPut, desirePath("delete::secrets:default:e"), adapter, deleteBody()).
		expect(t, http.StatusCreated, "")

	for _, item := range listItems(t, a.do(t, http.MethodGet, listPath(), adapter, "").object(t)) {
		ref, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("list item = %v, want an object", item)
		}
		version, present := ref[fieldTargetVersion]
		switch ref[fieldType] {
		case typeRead:
			if version != "v1" {
				t.Fatalf("read reference target_version = %v, want v1", version)
			}
		default:
			if present {
				t.Fatalf("%v reference carries target_version %v, want none", ref[fieldType], version)
			}
		}
	}
}

func TestListOfAnEmptyPartition(t *testing.T) {
	a := newTestAPI(t)
	res := a.do(t, http.MethodGet, listPath(), a.applier(t), "")
	res.expect(t, http.StatusOK, "")
	if string(res.body) != `{"items":[]}` {
		t.Fatalf("empty list = %s, want {\"items\":[]}", res.body)
	}
}

func TestListAcceptsTheMaximumLimit(t *testing.T) {
	a := newTestAPI(t)
	path := fmt.Sprintf("%s?limit=%d", listPath(), services.MaxListLimit)
	a.do(t, http.MethodGet, path, a.adapter(t), "").expect(t, http.StatusOK, "")
}

func TestListQueryValidation(t *testing.T) {
	a := newTestAPI(t)
	tests := map[string]string{
		"limit above maximum": "?limit=1001",
		"zero limit":          "?limit=0",
		"non-integer limit":   "?limit=ten",
		"unknown type":        "?type=patch",
		"undecodable cursor":  "?cursor=%25%25",
		"non-JSON cursor":     "?cursor=bm90LWpzb24",
	}
	fields := map[string]string{
		"limit above maximum": fieldLimit, "zero limit": fieldLimit, "non-integer limit": fieldLimit,
		"unknown type": fieldType, "undecodable cursor": fieldCursor, "non-JSON cursor": fieldCursor,
	}
	for name, query := range tests {
		t.Run(name, func(t *testing.T) {
			res := a.do(t, http.MethodGet, listPath()+query, a.adapter(t), "")
			res.expect(t, http.StatusBadRequest, errors.CodeInvalidQuery)
			assertField(t, res.problem(t), fields[name])
		})
	}
}
