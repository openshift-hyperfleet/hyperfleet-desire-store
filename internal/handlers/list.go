package handlers

import (
	"fmt"
	"net/url"
	"strconv"

	"github.com/openshift-hyperfleet/hyperfleet-applier/pkg/desire"

	"github.com/openshift-hyperfleet/hyperfleet-desire-store/internal/errors"
	"github.com/openshift-hyperfleet/hyperfleet-desire-store/internal/presenters"
	"github.com/openshift-hyperfleet/hyperfleet-desire-store/internal/services"
)

// decodeListQuery reads the type, limit, and cursor query parameters.
func decodeListQuery(query url.Values) (services.ListQuery, *errors.ServiceError) {
	parsed := services.ListQuery{Types: desire.AllTypes(), Limit: services.DefaultListLimit}

	if query.Has(fieldType) {
		switch desireType := desire.DesireType(query.Get(fieldType)); desireType {
		case desire.TypeApply, desire.TypeDelete, desire.TypeRead:
			parsed.Types = []desire.DesireType{desireType}
		default:
			return services.ListQuery{}, errors.Invalid(errors.CodeInvalidQuery, fieldType, errors.ConstraintEnum,
				"type must be apply, delete, or read")
		}
	}

	if query.Has(fieldLimit) {
		limit, err := strconv.Atoi(query.Get(fieldLimit))
		switch {
		case err != nil:
			return services.ListQuery{}, errors.Invalid(errors.CodeInvalidQuery, fieldLimit, errors.ConstraintFormat,
				"limit must be an integer")
		case limit < 1:
			return services.ListQuery{}, errors.Invalid(errors.CodeInvalidQuery, fieldLimit, errors.ConstraintMin,
				"limit must be at least 1")
		case limit > services.MaxListLimit:
			return services.ListQuery{}, errors.Invalid(errors.CodeInvalidQuery, fieldLimit, errors.ConstraintMax,
				fmt.Sprintf("limit must be at most %d", services.MaxListLimit))
		}
		parsed.Limit = limit
	}

	if query.Has(fieldCursor) {
		key, err := presenters.DecodeCursor(query.Get(fieldCursor))
		if err != nil {
			return services.ListQuery{}, errors.Invalid(errors.CodeInvalidQuery, fieldCursor, errors.ConstraintFormat,
				"cursor must be the next_cursor of a previous page")
		}
		parsed.After = &key
	}
	return parsed, nil
}
