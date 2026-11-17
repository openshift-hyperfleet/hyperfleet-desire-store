package handlers

import (
	"encoding/base64"
	"encoding/json"
	"net/url"
	"strconv"

	"github.com/openshift-hyperfleet/hyperfleet-applier/pkg/desire"

	"github.com/openshift-hyperfleet/hyperfleet-desire-store/internal/errors"
	"github.com/openshift-hyperfleet/hyperfleet-desire-store/internal/services"
)

// decodeListQuery reads the type, limit, and cursor query parameters, which
// validateRequests has checked against the contract.
func decodeListQuery(query url.Values) (services.ListQuery, *errors.ServiceError) {
	parsed := services.ListQuery{Types: desire.AllTypes(), Limit: services.DefaultListLimit}
	if query.Has(fieldType) {
		parsed.Types = []desire.DesireType{desire.DesireType(query.Get(fieldType))}
	}
	if query.Has(fieldLimit) {
		limit, err := strconv.Atoi(query.Get(fieldLimit))
		if err != nil {
			return services.ListQuery{}, errors.Invalid(errors.CodeInvalidQuery, fieldLimit, errors.ConstraintFormat,
				"limit must be an integer").WithCause(err)
		}
		parsed.Limit = limit
	}
	if query.Has(fieldCursor) {
		key, err := decodeCursor(query.Get(fieldCursor))
		if err != nil {
			return services.ListQuery{}, errors.Invalid(errors.CodeInvalidQuery, fieldCursor, errors.ConstraintFormat,
				"cursor must be the next_cursor of a previous page")
		}
		parsed.After = &key
	}
	return parsed, nil
}

// decodeCursor reads a cursor that presenters.EncodeCursor produced.
func decodeCursor(cursor string) (services.ListKey, error) {
	var key services.ListKey
	data, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return key, err
	}
	err = json.Unmarshal(data, &key)
	return key, err
}
