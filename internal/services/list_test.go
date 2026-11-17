package services

import (
	"testing"

	"github.com/openshift-hyperfleet/hyperfleet-applier/pkg/desire"
)

// TestPageOfDefaultsANonPositiveLimit covers a caller of the service that
// leaves Limit unset: it gets the default page size instead of a panic.
func TestPageOfDefaultsANonPositiveLimit(t *testing.T) {
	refs := []Reference{{Identity: desire.Identity{Type: desire.TypeApply, Resource: "configmaps", Name: "a"}}}

	page := pageOf(refs, ListQuery{Limit: 0})

	if len(page.Items) != 1 || page.Next != nil {
		t.Fatalf("page = %+v, want the one reference and no next key", page)
	}
}
