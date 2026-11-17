package handlers

import (
	"bytes"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"

	"github.com/openshift-hyperfleet/hyperfleet-applier/pkg/desire"

	"github.com/openshift-hyperfleet/hyperfleet-desire-store/internal/errors"
	"github.com/openshift-hyperfleet/hyperfleet-desire-store/internal/services"
)

// Request size limit. A desire carries one rendered Kubernetes manifest,
// which etcd caps at 1.5 MiB; the limit leaves room for the envelope.
const maxBodyBytes = 2 << 20

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

// readObject reads a request body that must be one JSON object, and returns
// its members undecoded. A member set to null is kept, so a field the request
// must not carry is rejected even when its value is null.
func readObject(body io.Reader) (map[string]json.RawMessage, *errors.ServiceError) {
	data, err := io.ReadAll(io.LimitReader(body, maxBodyBytes+1))
	if err != nil {
		return nil, errors.Invalid(errors.CodeInvalidBody, fieldBody, errors.ConstraintFormat,
			"the request body could not be read")
	}
	if len(data) > maxBodyBytes {
		return nil, errors.Invalid(errors.CodeInvalidBody, fieldBody, errors.ConstraintMaxLength,
			fmt.Sprintf("the request body exceeds %d bytes", maxBodyBytes))
	}
	var members map[string]json.RawMessage
	err = json.Unmarshal(data, &members)
	if _, ok := stderrors.AsType[*json.SyntaxError](err); ok {
		return nil, errors.Invalid(errors.CodeInvalidBody, fieldBody, errors.ConstraintFormat,
			"the request body is not valid JSON")
	}
	if err != nil || !isObject(data) {
		return nil, errors.Invalid(errors.CodeInvalidBody, fieldBody, errors.ConstraintFormat,
			"the request body must be a JSON object")
	}
	return members, nil
}

// decodeWrite validates a Write desire body for the identity's type.
func decodeWrite(body io.Reader, id desire.Identity) (services.WriteInput, *errors.ServiceError) {
	members, problem := readObject(body)
	if problem != nil {
		return services.WriteInput{}, problem
	}
	if _, ok := members[fieldOwner]; ok {
		return services.WriteInput{}, errors.Invalid(errors.CodeInvalidField, fieldOwner, "",
			"owner must not be sent; the API sets it from the caller")
	}

	var request services.WriteInput
	if request.ResourceID, problem = requiredString(members, fieldResourceID); problem != nil {
		return services.WriteInput{}, problem
	}
	if raw, ok := members[fieldVersion]; ok {
		if request.Version, problem = bodyInteger(fieldVersion, raw, 0); problem != nil {
			return services.WriteInput{}, problem
		}
	}

	switch id.Type {
	case desire.TypeApply:
		if request.KubeContent, problem = decodeKubeContent(members); problem == nil {
			problem = checkKubeContentIdentity(request.KubeContent, id)
		}
	case desire.TypeRead:
		if problem = checkNoUpdate(members, id.Type, request.Version); problem == nil {
			request.TargetVersion, problem = requiredString(members, fieldTargetVersion)
		}
	case desire.TypeDelete:
		problem = checkNoUpdate(members, id.Type, request.Version)
	}
	if problem != nil {
		return services.WriteInput{}, problem
	}
	return request, nil
}

// requiredString decodes a member that must be a non-empty string.
func requiredString(members map[string]json.RawMessage, field string) (string, *errors.ServiceError) {
	raw, ok := members[field]
	if !ok {
		return "", errors.Invalid(errors.CodeRequired, field, errors.ConstraintRequired, field+" is required")
	}
	var value string
	if isNull(raw) || json.Unmarshal(raw, &value) != nil {
		return "", errors.Invalid(errors.CodeInvalidField, field, errors.ConstraintFormat, field+" must be a string")
	}
	if value == "" {
		return "", errors.Invalid(errors.CodeInvalidField, field, errors.ConstraintMinLength, field+" must not be empty")
	}
	return value, nil
}

// decodeKubeContent returns the manifest that an ApplyDesire write requires.
func decodeKubeContent(members map[string]json.RawMessage) (json.RawMessage, *errors.ServiceError) {
	kubeContent, ok := members[fieldKubeContent]
	if !ok {
		return nil, errors.Invalid(errors.CodeRequired, fieldKubeContent, errors.ConstraintRequired,
			"kubeContent is required for an ApplyDesire")
	}
	if !isObject(kubeContent) {
		return nil, errors.Invalid(errors.CodeInvalidField, fieldKubeContent, errors.ConstraintFormat,
			"kubeContent must be a JSON object")
	}
	return kubeContent, nil
}

// checkNoUpdate rejects the fields a ReadDesire or DeleteDesire write must
// not carry: a manifest, and a non-zero version, since neither type has an
// update.
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
func checkKubeContentIdentity(kubeContent json.RawMessage, id desire.Identity) *errors.ServiceError {
	var manifest map[string]json.RawMessage
	if err := json.Unmarshal(kubeContent, &manifest); err != nil {
		return errors.Unprocessable(fieldKubeContent, errors.ConstraintFormat, "kubeContent must be a JSON object")
	}
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

	var metadata map[string]json.RawMessage
	if raw, ok := manifest["metadata"]; ok && !isNull(raw) {
		if !isObject(raw) || json.Unmarshal(raw, &metadata) != nil {
			return errors.Unprocessable(fieldManifestMetadata, errors.ConstraintFormat,
				"kubeContent.metadata must be a JSON object")
		}
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
	if !ok || isNull(raw) {
		return "", nil
	}
	var value string
	if json.Unmarshal(raw, &value) != nil {
		return "", errors.Unprocessable(field, errors.ConstraintFormat, field+" must be a string")
	}
	return value, nil
}

// decodeStatus validates a Write status body for the identity's type.
func decodeStatus(body io.Reader, id desire.Identity) (services.StatusInput, *errors.ServiceError) {
	members, problem := readObject(body)
	if problem != nil {
		return services.StatusInput{}, problem
	}
	var request services.StatusInput

	raw, ok := members[fieldStatus]
	if !ok {
		return services.StatusInput{}, errors.Invalid(errors.CodeRequired, fieldStatus, errors.ConstraintRequired,
			"status is required")
	}
	if !isObject(raw) {
		return services.StatusInput{}, errors.Invalid(errors.CodeInvalidField, fieldStatus, errors.ConstraintFormat,
			"status must be a JSON object")
	}
	target := any(&request.Status)
	if id.Type == desire.TypeRead {
		target = &request.ReadStatus
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return services.StatusInput{}, errors.Invalid(errors.CodeInvalidField, fieldStatus, errors.ConstraintFormat,
			"status does not match the desire status shape").WithCause(err)
	}

	if id.Type == desire.TypeRead {
		return request, nil
	}
	rawVersion, ok := members[fieldVersion]
	if !ok {
		return services.StatusInput{}, errors.Invalid(errors.CodeRequired, fieldVersion, errors.ConstraintRequired,
			fmt.Sprintf("version is required on a %s desire status write", id.Type))
	}
	version, problem := bodyInteger(fieldVersion, rawVersion, 1)
	if problem != nil {
		return services.StatusInput{}, problem
	}
	request.Version = version
	return request, nil
}

// bodyInteger decodes a JSON integer member that must be at least minimum.
func bodyInteger(field string, raw json.RawMessage, minimum int64) (int64, *errors.ServiceError) {
	value, err := strconv.ParseInt(string(bytes.TrimSpace(raw)), 10, 64)
	if err != nil {
		return 0, errors.Invalid(errors.CodeInvalidField, field, errors.ConstraintFormat, field+" must be an integer")
	}
	if value < minimum {
		return 0, errors.Invalid(errors.CodeInvalidField, field, errors.ConstraintMin,
			fmt.Sprintf("%s must be at least %d", field, minimum))
	}
	return value, nil
}

// decodeDeleteVersion reads the required version query parameter of a delete.
func decodeDeleteVersion(query url.Values) (int64, *errors.ServiceError) {
	if !query.Has(fieldVersion) {
		return 0, errors.Invalid(errors.CodeRequired, fieldVersion, errors.ConstraintRequired, "version is required")
	}
	version, err := strconv.ParseInt(query.Get(fieldVersion), 10, 64)
	if err != nil {
		return 0, errors.Invalid(errors.CodeInvalidQuery, fieldVersion, errors.ConstraintFormat, "version must be an integer")
	}
	if version < 1 {
		return 0, errors.Invalid(errors.CodeInvalidQuery, fieldVersion, errors.ConstraintMin, "version must be at least 1")
	}
	return version, nil
}

func isObject(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && trimmed[0] == '{'
}

func isNull(raw json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}
