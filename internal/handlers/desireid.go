package handlers

import (
	"strings"

	"github.com/openshift-hyperfleet/hyperfleet-applier/pkg/desire"

	"github.com/openshift-hyperfleet/hyperfleet-desire-store/internal/errors"
)

const fieldDesireID = "desire-id"

// parseDesireID decodes a desire identifier in a partition. The first four
// segments are type, group, resource, and namespace; the rest, colons
// included, is the name. The shared store's identity validation then checks
// every field. It is stricter than the DesireId pattern of the REST v1
// specification, so an identifier it accepts also matches that pattern, and
// the store never sees an identity it would reject. Its message names the
// store's fields, so the client gets a fixed one.
func parseDesireID(raw, partition string) (desire.Identity, *errors.ServiceError) {
	segments := strings.SplitN(raw, ":", 5)
	if len(segments) != 5 || segments[4] == "" {
		return desire.Identity{}, errors.Invalid(errors.CodeInvalidPath, fieldDesireID, errors.ConstraintPattern,
			"desire-id must be type:group:resource:namespace:name with a non-empty name")
	}
	id := desire.Identity{
		ManagementCluster: partition,
		Type:              desire.DesireType(segments[0]),
		Group:             segments[1],
		Resource:          segments[2],
		Namespace:         segments[3],
		Name:              segments[4],
	}
	if err := id.Validate(); err != nil {
		return desire.Identity{}, errors.Invalid(errors.CodeInvalidPath, fieldDesireID, errors.ConstraintPattern,
			"desire-id type must be apply, delete, or read, and its other segments valid Kubernetes names").
			WithCause(err)
	}
	return id, nil
}
