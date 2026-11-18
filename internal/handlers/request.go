package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"

	"github.com/openshift-hyperfleet/hyperfleet-applier/pkg/desire"

	"github.com/openshift-hyperfleet/hyperfleet-desire-store/internal/errors"
	"github.com/openshift-hyperfleet/hyperfleet-desire-store/internal/services"
	"github.com/openshift-hyperfleet/hyperfleet-desire-store/pkg/api/openapi"
)

// Request fields and query parameters, as problem errors name them.
const (
	fieldBody          = "body"
	fieldResourceID    = "resource_id"
	fieldVersion       = "version"
	fieldKubeContent   = "kubeContent"
	fieldTargetVersion = "target_version"
	fieldStatus        = "status"
	fieldOwner         = "owner"
	fieldLimit         = "limit"
	fieldType          = "type"
	fieldCursor        = "cursor"

	fieldManifestAPIVersion = "kubeContent.apiVersion"
	fieldManifestMetadata   = "kubeContent.metadata"
	fieldManifestNamespace  = "kubeContent.metadata.namespace"
	fieldManifestName       = "kubeContent.metadata.name"
)

// decodeBody decodes a request body, which validateRequests has checked
// against the contract, into the contract type T. It also returns the body's
// top-level members, for the rules that depend on whether a member is sent.
// Members that T does not declare are ignored: the contract lets a newer
// client send optional additive fields.
func decodeBody[T any](body io.Reader) (*T, map[string]json.RawMessage, *errors.ServiceError) {
	data, err := io.ReadAll(body)
	if err != nil {
		return nil, nil, errors.Invalid(errors.CodeInvalidBody, fieldBody, errors.ConstraintFormat,
			"the request body could not be read").WithCause(err)
	}
	// The validator reads only the first JSON value, so trailing data
	// fails here.
	var members map[string]json.RawMessage
	if err := json.Unmarshal(data, &members); err != nil {
		return nil, nil, errors.Invalid(errors.CodeInvalidBody, fieldBody, errors.ConstraintFormat,
			"the request body is not valid JSON").WithCause(err)
	}
	var request T
	if err := json.Unmarshal(data, &request); err != nil {
		return nil, nil, errors.Invalid(errors.CodeInvalidBody, fieldBody, errors.ConstraintFormat,
			"the request body does not match the request schema").WithCause(err)
	}
	return &request, members, nil
}

// decodeWrite applies the Write desire rules that the contract states only
// in prose: owner is never sent, and each desire type has its own fields.
func decodeWrite(body io.Reader, id desire.Identity) (services.WriteInput, *errors.ServiceError) {
	request, members, problem := decodeBody[openapi.DesireWrite](body)
	if problem != nil {
		return services.WriteInput{}, problem
	}
	if _, ok := members[fieldOwner]; ok {
		return services.WriteInput{}, errors.Invalid(errors.CodeInvalidField, fieldOwner, "",
			"owner must not be sent; the API sets it from the caller")
	}
	// TODO(HYPERFLEET-1825): remove this check and the target_version check
	// below when the service moves to spec v0.2.2, which adds minLength 1 to
	// both fields (hyperfleet-desire-store-spec#4).
	if request.ResourceId == "" {
		return services.WriteInput{}, errors.Invalid(errors.CodeInvalidField, fieldResourceID,
			errors.ConstraintMinLength, "resource_id must not be empty")
	}
	input := services.WriteInput{ResourceID: request.ResourceId, Version: request.Version}

	switch id.Type {
	case desire.TypeApply:
		input.KubeContent, problem = decodeKubeContent(request.KubeContent, members, id)
	case desire.TypeRead:
		problem = checkNoUpdate(members, id.Type, input.Version)
		if _, ok := members[fieldTargetVersion]; problem == nil && !ok {
			problem = errors.Invalid(errors.CodeRequired, fieldTargetVersion, errors.ConstraintRequired,
				"target_version is required")
		}
		// TODO(HYPERFLEET-1825): remove with the resource_id check above.
		if problem == nil && request.TargetVersion == "" {
			problem = errors.Invalid(errors.CodeInvalidField, fieldTargetVersion, errors.ConstraintMinLength,
				"target_version must not be empty")
		}
		input.TargetVersion = request.TargetVersion
	case desire.TypeDelete:
		problem = checkNoUpdate(members, id.Type, input.Version)
	}
	if problem != nil {
		return services.WriteInput{}, problem
	}
	return input, nil
}

// decodeKubeContent returns the manifest that an ApplyDesire write requires,
// as the bytes the client sent, after it checks the manifest against the
// path.
func decodeKubeContent(
	kubeContent json.RawMessage, members map[string]json.RawMessage, id desire.Identity,
) ([]byte, *errors.ServiceError) {
	if _, ok := members[fieldKubeContent]; !ok {
		return nil, errors.Invalid(errors.CodeRequired, fieldKubeContent, errors.ConstraintRequired,
			"kubeContent is required for an ApplyDesire")
	}
	var manifest map[string]json.RawMessage
	if err := json.Unmarshal(kubeContent, &manifest); err != nil {
		return nil, errors.Unprocessable(fieldKubeContent, errors.ConstraintFormat, "kubeContent must be a JSON object")
	}
	if problem := checkKubeContentIdentity(manifest, id); problem != nil {
		return nil, problem
	}
	return kubeContent, nil
}

// checkNoUpdate rejects the fields a ReadDesire or DeleteDesire write must
// not carry: a manifest, even a null one, and a non-zero version, since
// neither type has an update.
func checkNoUpdate(
	members map[string]json.RawMessage, desireType desire.DesireType, version int64,
) *errors.ServiceError {
	if _, ok := members[fieldKubeContent]; ok {
		return errors.Invalid(errors.CodeInvalidField, fieldKubeContent, "",
			fmt.Sprintf("kubeContent must not be sent on a %s desire", desireType))
	}
	if version != 0 {
		return errors.Invalid(errors.CodeInvalidField, fieldVersion, errors.ConstraintMax,
			fmt.Sprintf("a %s desire has no update, so version must be 0 or omitted", desireType))
	}
	return nil
}

// checkKubeContentIdentity rejects a manifest whose Kubernetes identity does
// not match the path. kind is not compared with resource: the hub has no
// discovery for the target clusters. Members are matched by their exact
// names, the way the Kubernetes API server reads them; a case-insensitive
// struct decode would let a "Metadata" member stand in for "metadata".
func checkKubeContentIdentity(manifest map[string]json.RawMessage, id desire.Identity) *errors.ServiceError {
	apiVersion, problem := manifestString(manifest, "apiVersion", fieldManifestAPIVersion)
	if problem != nil {
		return problem
	}
	if apiVersion == "" {
		return errors.Unprocessable(fieldManifestAPIVersion, errors.ConstraintRequired, "kubeContent.apiVersion is required")
	}
	group := ""
	if before, _, found := strings.Cut(apiVersion, "/"); found {
		group = before
	}
	if group != id.Group {
		return errors.Unprocessable(fieldManifestAPIVersion, "",
			fmt.Sprintf("kubeContent apiVersion %q is in group %q, but the path names group %q",
				apiVersion, group, id.Group))
	}

	// A null metadata decodes as an empty one.
	var metadata map[string]json.RawMessage
	if raw, ok := manifest["metadata"]; ok && json.Unmarshal(raw, &metadata) != nil {
		return errors.Unprocessable(fieldManifestMetadata, errors.ConstraintFormat,
			"kubeContent.metadata must be a JSON object")
	}
	namespace, problem := manifestString(metadata, "namespace", fieldManifestNamespace)
	if problem != nil {
		return problem
	}
	if namespace != id.Namespace {
		return errors.Unprocessable(fieldManifestNamespace, "",
			fmt.Sprintf("kubeContent names namespace %q, but the path names %q", namespace, id.Namespace))
	}
	name, problem := manifestString(metadata, "name", fieldManifestName)
	if problem != nil {
		return problem
	}
	if name != id.Name {
		return errors.Unprocessable(fieldManifestName, "",
			fmt.Sprintf("kubeContent names %q, but the path names %q", name, id.Name))
	}
	return nil
}

// manifestString reads one string member of a manifest object. An absent or
// null member reads as empty.
func manifestString(members map[string]json.RawMessage, key, field string) (string, *errors.ServiceError) {
	raw, ok := members[key]
	if !ok {
		return "", nil
	}
	var value *string
	if json.Unmarshal(raw, &value) != nil {
		return "", errors.Unprocessable(field, errors.ConstraintFormat, field+" must be a string")
	}
	if value == nil {
		return "", nil
	}
	return *value, nil
}

// decodeStatus applies the Write status rules that the contract states only
// in prose: the status has the desire type's shape, and an ApplyDesire or
// DeleteDesire status write carries a version.
func decodeStatus(body io.Reader, id desire.Identity) (services.StatusInput, *errors.ServiceError) {
	request, members, problem := decodeBody[openapi.StatusWrite](body)
	if problem != nil {
		return services.StatusInput{}, problem
	}

	var input services.StatusInput
	target := any(&input.Status)
	if id.Type == desire.TypeRead {
		target = &input.ReadStatus
	}
	if err := json.Unmarshal(request.Status, target); err != nil {
		return services.StatusInput{}, errors.Invalid(errors.CodeInvalidField, fieldStatus, errors.ConstraintFormat,
			"status does not match the desire status shape").WithCause(err)
	}

	if id.Type == desire.TypeRead {
		return input, nil
	}
	if _, ok := members[fieldVersion]; !ok {
		return services.StatusInput{}, errors.Invalid(errors.CodeRequired, fieldVersion, errors.ConstraintRequired,
			fmt.Sprintf("version is required on a %s desire status write", id.Type))
	}
	input.Version = request.Version
	return input, nil
}

// decodeDeleteVersion reads the version query parameter of a delete, which
// validateRequests has checked against the contract.
func decodeDeleteVersion(query url.Values) (int64, *errors.ServiceError) {
	version, err := strconv.ParseInt(query.Get(fieldVersion), 10, 64)
	if err != nil {
		return 0, errors.Invalid(errors.CodeInvalidQuery, fieldVersion, errors.ConstraintFormat,
			"version must be an integer").WithCause(err)
	}
	return version, nil
}
