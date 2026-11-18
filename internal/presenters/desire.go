// Package presenters turns service results into the REST v1 wire shapes.
package presenters

import (
	"encoding/base64"
	"encoding/json"
	"errors"

	"github.com/openshift-hyperfleet/hyperfleet-applier/pkg/desire"

	"github.com/openshift-hyperfleet/hyperfleet-desire-store/internal/services"
	"github.com/openshift-hyperfleet/hyperfleet-desire-store/pkg/api/openapi"
)

// PresentDesire returns the wire shape of a stored desire. A status the
// Applier has not written yet is left out. kubeContent is returned as the
// bytes that were stored.
func PresentDesire(d services.Desire) (openapi.Desire, error) {
	switch {
	case d.Apply != nil:
		out := common(d.Apply.Identity, d.Apply.Owner, d.Apply.OriginID, d.Apply.Generation, d.Apply.Version)
		out.KubeContent = d.Apply.Spec.KubeContent
		var err error
		if len(d.Apply.Status.Conditions) > 0 {
			out.Status, err = json.Marshal(d.Apply.Status)
		}
		return out, err
	case d.Delete != nil:
		out := common(d.Delete.Identity, d.Delete.Owner, d.Delete.OriginID, d.Delete.Generation, d.Delete.Version)
		var err error
		if len(d.Delete.Status.Conditions) > 0 {
			out.Status, err = json.Marshal(d.Delete.Status)
		}
		return out, err
	case d.Read != nil:
		out := common(d.Read.Identity, d.Read.Owner, d.Read.OriginID, d.Read.Generation, d.Read.Version)
		out.TargetVersion = d.Read.TargetVersion
		var err error
		if len(d.Read.Status.Conditions) > 0 || len(d.Read.Status.KubeContent) > 0 {
			out.Status, err = json.Marshal(d.Read.Status)
		}
		return out, err
	default:
		return openapi.Desire{}, errors.New("the stored desire has no type")
	}
}

// PresentList returns the wire shape of a List page, with the cursor of the
// next page when one remains. A ReadDesire reference also carries
// target_version, so the Applier's read poll can watch the object at that
// version without a Get of every ReadDesire.
func PresentList(page services.Page) (openapi.DesireList, error) {
	out := openapi.DesireList{Items: make([]openapi.DesireReference, 0, len(page.Items))}
	for _, ref := range page.Items {
		id := ref.Identity
		out.Items = append(out.Items, openapi.DesireReference{
			Type: openapi.DesireType(id.Type), Group: id.Group, Resource: id.Resource, Namespace: id.Namespace,
			Name: id.Name, Version: ref.Version, TargetVersion: ref.TargetVersion,
		})
	}
	if page.Next != nil {
		cursor, err := EncodeCursor(*page.Next)
		if err != nil {
			return openapi.DesireList{}, err
		}
		out.NextCursor = cursor
	}
	return out, nil
}

// EncodeCursor turns a pagination key into the opaque next_cursor value: the
// key as a JSON array, base64url-encoded so the cursor is safe in a query
// string whatever characters a name holds.
func EncodeCursor(key services.ListKey) (string, error) {
	data, err := json.Marshal(key)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func common(id desire.Identity, owner, originID string, generation, version int64) openapi.Desire {
	return openapi.Desire{
		Id:         services.FormatDesireID(id),
		Type:       openapi.DesireType(id.Type),
		Group:      id.Group,
		Resource:   id.Resource,
		Namespace:  id.Namespace,
		Name:       id.Name,
		Owner:      owner,
		ResourceId: originID,
		Generation: generation,
		Version:    version,
	}
}
