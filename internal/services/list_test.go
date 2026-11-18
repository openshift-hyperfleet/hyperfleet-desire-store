package services

import (
	"slices"
	"testing"

	"github.com/openshift-hyperfleet/hyperfleet-applier/pkg/desire"
)

// TestPageOfDefaultsANonPositiveLimit covers a caller of the service that
// leaves Limit unset: it gets the default page size instead of a panic.
// TestPageOfFullLastPage covers the boundary of the page check: a page that
// holds exactly the remaining references has no next key.
func TestPageOfFullLastPage(t *testing.T) {
	var refs []Reference
	for _, name := range []string{"a", "b", "c"} {
		refs = append(refs, Reference{Identity: desire.Identity{Type: desire.TypeApply, Resource: "configmaps", Name: name}})
	}

	if page := pageOf(slices.Clone(refs), ListQuery{Limit: 3}); len(page.Items) != 3 || page.Next != nil {
		t.Fatalf("page = %+v, want all three references and no next key", page)
	}
	if page := pageOf(slices.Clone(refs), ListQuery{Limit: 2}); len(page.Items) != 2 || page.Next == nil {
		t.Fatalf("page = %+v, want two references and a next key", page)
	}
}

func TestPageOfDefaultsANonPositiveLimit(t *testing.T) {
	refs := []Reference{{Identity: desire.Identity{Type: desire.TypeApply, Resource: "configmaps", Name: "a"}}}

	page := pageOf(refs, ListQuery{Limit: 0})

	if len(page.Items) != 1 || page.Next != nil {
		t.Fatalf("page = %+v, want the one reference and no next key", page)
	}
}
