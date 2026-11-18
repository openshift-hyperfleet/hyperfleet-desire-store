package services

import (
	"slices"

	"github.com/openshift-hyperfleet/hyperfleet-applier/pkg/desire"
)

// List page sizes, from the REST v1 specification.
const (
	DefaultListLimit = 100
	MaxListLimit     = 1000
)

// ListQuery is a decoded List desires request.
type ListQuery struct {
	// Types are the desire types to list.
	Types []desire.DesireType
	// Limit is the page size. A Limit below 1 means DefaultListLimit.
	Limit int
	// After is the key of the last desire of the previous page, or nil for
	// the first page.
	After *ListKey
}

// Reference is one entry of a List page.
type Reference struct {
	Identity      desire.Identity
	Version       int64
	TargetVersion string
}

// Page is one List page. Next is the key to continue after, or nil on the
// last page.
type Page struct {
	Items []Reference
	Next  *ListKey
}

// ListKey orders desires for keyset pagination: type, group, resource,
// namespace, name.
type ListKey [5]string

// KeyOf returns the pagination key of a reference.
func KeyOf(ref Reference) ListKey {
	id := ref.Identity
	return ListKey{string(id.Type), id.Group, id.Resource, id.Namespace, id.Name}
}

func (k ListKey) compare(other ListKey) int { return slices.Compare(k[:], other[:]) }

// pageOf sorts the references, skips those up to the query's key, and returns
// one page. The store has no paging, so every page reads the whole
// partition.
func pageOf(refs []Reference, query ListQuery) Page {
	if query.Limit < 1 {
		query.Limit = DefaultListLimit
	}
	slices.SortFunc(refs, func(a, b Reference) int { return KeyOf(a).compare(KeyOf(b)) })
	if query.After != nil {
		after := *query.After
		start := slices.IndexFunc(refs, func(ref Reference) bool { return KeyOf(ref).compare(after) > 0 })
		if start < 0 {
			start = len(refs)
		}
		refs = refs[start:]
	}
	page := Page{Items: refs}
	if len(refs) > query.Limit {
		page.Items = refs[:query.Limit]
		page.Next = new(KeyOf(page.Items[len(page.Items)-1]))
	}
	return page
}
