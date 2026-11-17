// Package presenters turns service results into the REST v1 wire shapes.
package presenters

import (
	"encoding/base64"
	"encoding/json"

	"github.com/openshift-hyperfleet/hyperfleet-applier/pkg/desire"

	"github.com/openshift-hyperfleet/hyperfleet-desire-store/internal/services"
)

// Desire is the Desire wire shape. kubeContent stays raw JSON, so the
// service returns exactly what was stored.
type Desire struct {
	ID            string          `json:"id"`
	Type          string          `json:"type"`
	Group         string          `json:"group"`
	Resource      string          `json:"resource"`
	Namespace     string          `json:"namespace"`
	Name          string          `json:"name"`
	Owner         string          `json:"owner"`
	ResourceID    string          `json:"resource_id"`
	KubeContent   json.RawMessage `json:"kubeContent,omitempty"`
	TargetVersion string          `json:"target_version,omitempty"`
	Status        any             `json:"status,omitempty"`
	Generation    int64           `json:"generation"`
	Version       int64           `json:"version"`
}

// Reference is the DesireReference wire shape that List returns. A ReadDesire
// reference also carries target_version, so the Applier's read poll can watch
// the object at that version without a Get of every ReadDesire. A recreated
// ReadDesire keeps its identity and version, so a changed target version
// shows only here.
type Reference struct {
	Type          string `json:"type"`
	Group         string `json:"group"`
	Resource      string `json:"resource"`
	Namespace     string `json:"namespace"`
	Name          string `json:"name"`
	Version       int64  `json:"version"`
	TargetVersion string `json:"target_version,omitempty"`
}

// List is the DesireList wire shape.
type List struct {
	Items      []Reference `json:"items"`
	NextCursor string      `json:"next_cursor,omitempty"`
}

// PresentDesire returns the wire shape of a stored desire. A status the
// Applier has not written yet is left out.
func PresentDesire(d services.Desire) Desire {
	switch {
	case d.Apply != nil:
		out := common(d.Apply.Identity, d.Apply.Owner, d.Apply.OriginID, d.Apply.Generation, d.Apply.Version)
		out.KubeContent = d.Apply.Spec.KubeContent
		out.Status = conditionsStatus(d.Apply.Status)
		return out
	case d.Delete != nil:
		out := common(d.Delete.Identity, d.Delete.Owner, d.Delete.OriginID, d.Delete.Generation, d.Delete.Version)
		out.Status = conditionsStatus(d.Delete.Status)
		return out
	case d.Read != nil:
		out := common(d.Read.Identity, d.Read.Owner, d.Read.OriginID, d.Read.Generation, d.Read.Version)
		out.TargetVersion = d.Read.TargetVersion
		if len(d.Read.Status.Conditions) > 0 || len(d.Read.Status.KubeContent) > 0 {
			out.Status = d.Read.Status
		}
		return out
	default:
		return Desire{}
	}
}

// PresentList returns the wire shape of a List page, with the cursor of the
// next page when one remains.
func PresentList(page services.Page) (List, error) {
	out := List{Items: make([]Reference, 0, len(page.Items))}
	for _, ref := range page.Items {
		id := ref.Identity
		out.Items = append(out.Items, Reference{
			Type: string(id.Type), Group: id.Group, Resource: id.Resource, Namespace: id.Namespace, Name: id.Name,
			Version: ref.Version, TargetVersion: ref.TargetVersion,
		})
	}
	if page.Next != nil {
		cursor, err := EncodeCursor(*page.Next)
		if err != nil {
			return List{}, err
		}
		out.NextCursor = cursor
	}
	return out, nil
}

// EncodeCursor turns a pagination key into the opaque next_cursor value.
func EncodeCursor(key services.ListKey) (string, error) {
	data, err := json.Marshal(key)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

// DecodeCursor reads a cursor that EncodeCursor produced.
func DecodeCursor(cursor string) (services.ListKey, error) {
	var key services.ListKey
	data, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return key, err
	}
	err = json.Unmarshal(data, &key)
	return key, err
}

func common(id desire.Identity, owner, originID string, generation, version int64) Desire {
	return Desire{
		ID:         services.FormatDesireID(id),
		Type:       string(id.Type),
		Group:      id.Group,
		Resource:   id.Resource,
		Namespace:  id.Namespace,
		Name:       id.Name,
		Owner:      owner,
		ResourceID: originID,
		Generation: generation,
		Version:    version,
	}
}

// conditionsStatus leaves out a status that the Applier has not written yet.
func conditionsStatus(status desire.Status) any {
	if len(status.Conditions) == 0 {
		return nil
	}
	return status
}
