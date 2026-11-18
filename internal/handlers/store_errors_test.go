package handlers

import (
	"context"
	stderrors "errors"
	"net/http"
	"testing"

	"github.com/openshift-hyperfleet/hyperfleet-applier/pkg/desire"
	"github.com/openshift-hyperfleet/hyperfleet-applier/pkg/desire/store/memory"

	"github.com/openshift-hyperfleet/hyperfleet-desire-store/internal/errors"
)

// failingStore answers apply gets, lists, and creates with err.
type failingStore struct {
	*memory.Store
	err error
}

func (s failingStore) GetApplyDesire(context.Context, desire.Identity) (desire.ApplyDesire, error) {
	return desire.ApplyDesire{}, s.err
}

func (s failingStore) ListApplyDesires(context.Context, string) ([]desire.ApplyDesire, error) {
	return nil, s.err
}

func (s failingStore) CreateApplyDesire(context.Context, desire.ApplyDesire) (desire.ApplyDesire, error) {
	return desire.ApplyDesire{}, s.err
}

func TestDegradedStoreReturns503(t *testing.T) {
	tests := map[string]struct {
		err  error
		code string
	}{
		"connection failure": {stderrors.New("dial tcp: connection refused"), errors.CodeDependencyFailure},
		"aborted":            {desire.ErrAborted, errors.CodeUnavailable},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			a := newTestAPI(t, func(o *apiOptions) { o.store = failingStore{Store: memory.New(), err: tc.err} })

			requests := []struct{ method, path, body string }{
				{http.MethodGet, desirePath(applyID), ""},
				{http.MethodGet, listPath(), ""},
				{http.MethodPut, desirePath(applyID), applyBody("coredns")},
			}
			for _, req := range requests {
				res := a.do(t, req.method, req.path, a.adapter(t), req.body)
				res.expect(t, http.StatusServiceUnavailable, tc.code)
				if got := res.problem(t).Type; got != errors.TypeBase+"service-unavailable" {
					t.Fatalf("type = %q, want service-unavailable", got)
				}
			}
		})
	}
}
