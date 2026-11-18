package handlers

import (
	"net/http"
	"testing"

	"github.com/openshift-hyperfleet/hyperfleet-desire-store/internal/auth"
	"github.com/openshift-hyperfleet/hyperfleet-desire-store/internal/errors"
)

// TestEveryCallerMayListAndGet covers the List and Get columns of the
// Caller-to-Operation matrix, which allow every caller in the partition.
func TestEveryCallerMayListAndGet(t *testing.T) {
	a := newTestAPI(t)
	a.do(t, http.MethodPut, desirePath(applyID), a.adapter(t), applyBody("coredns")).expect(t, http.StatusCreated, "")

	for name, token := range map[string]string{
		"applier": a.applier(t), "adapter": a.adapter(t), "sweeper": a.sweeper(t),
	} {
		t.Run(name, func(t *testing.T) {
			a.do(t, http.MethodGet, listPath(), token, "").expect(t, http.StatusOK, "")
			a.do(t, http.MethodGet, desirePath(applyID), token, "").expect(t, http.StatusOK, "")
		})
	}
}

// TestEachHandlerChecksItsOperation sends one request the matrix denies to
// each handler that changes state, so a handler that checks the wrong
// operation fails here.
func TestEachHandlerChecksItsOperation(t *testing.T) {
	a := newTestAPI(t)
	a.do(t, http.MethodPut, desirePath(applyID), a.adapter(t), applyBody("coredns")).expect(t, http.StatusCreated, "")

	a.do(t, http.MethodPut, desirePath(applyID), a.applier(t), withVersion(applyBody("coredns"), 1)).
		expect(t, http.StatusForbidden, errors.CodePermissionDenied)
	a.do(t, http.MethodPut, statusPath(applyID), a.adapter(t), appliedStatus(1)).
		expect(t, http.StatusForbidden, errors.CodePermissionDenied)
	a.do(t, http.MethodDelete, desirePath(applyID)+"?version=1", a.applier(t), "").
		expect(t, http.StatusForbidden, errors.CodePermissionDenied)

	a.do(t, http.MethodGet, desirePath(applyID), a.adapter(t), "").expect(t, http.StatusOK, "")
}

// TestForgedHeadersGrantNothing is ISO-3: client headers that name another
// partition, identity, or caller class change nothing.
func TestForgedHeadersGrantNothing(t *testing.T) {
	a := newTestAPI(t)
	applier := a.applier(t)
	forged := [][2]string{
		{"X-Hf-Partition", "mc-02"},
		{"X-Hf-Identity", adapterA},
		{"X-Hf-Caller-Class", "fleet-scoped"},
		{"Authorization", "Bearer " + a.token(t, auth.ClassFleetScoped, adapterA, "mc-02")},
	}

	a.do(t, http.MethodGet, partitionDesirePath("mc-02", applyID), applier, "", forged...).
		expect(t, http.StatusForbidden, errors.CodePartitionDenied)
	a.do(t, http.MethodGet, "/api/hyperfleet/desire-store/v1/partitions/mc-02/desires", applier, "", forged...).
		expect(t, http.StatusForbidden, errors.CodePartitionDenied)
	a.do(t, http.MethodPut, partitionDesirePath("mc-02", applyID), applier, applyBody("coredns"), forged...).
		expect(t, http.StatusForbidden, errors.CodePartitionDenied)
	a.do(t, http.MethodPut, desirePath(applyID), applier, applyBody("coredns"), forged...).
		expect(t, http.StatusForbidden, errors.CodePermissionDenied)

	a.do(t, http.MethodGet, desirePath(applyID), "", "", forged...).expect(t, http.StatusUnauthorized, auth.CodeMissing)
}

// TestThePartitionComesFromTheClaim shows that the store receives the claim's
// partition: a desire written in mc-01 is invisible to an mc-02 caller.
func TestThePartitionComesFromTheClaim(t *testing.T) {
	a := newTestAPI(t)
	a.do(t, http.MethodPut, desirePath(applyID), a.adapter(t), applyBody("coredns")).expect(t, http.StatusCreated, "")

	other := a.token(t, auth.ClassRemoteApplier, applierID, "mc-02")
	a.do(t, http.MethodGet, partitionDesirePath("mc-02", applyID), other, "").
		expect(t, http.StatusNotFound, errors.CodeNotFound)
	res := a.do(t, http.MethodGet, "/api/hyperfleet/desire-store/v1/partitions/mc-02/desires", other, "")
	res.expect(t, http.StatusOK, "")
	if string(res.body) != `{"items":[]}` {
		t.Fatalf("mc-02 list = %s, want no items", res.body)
	}
}
