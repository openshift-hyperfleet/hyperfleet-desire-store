package handlers

import (
	"context"
	stderrors "errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	nethttpmiddleware "github.com/oapi-codegen/nethttp-middleware"
	dsschemas "github.com/openshift-hyperfleet/hyperfleet-desire-store-spec/schemas"

	"github.com/openshift-hyperfleet/hyperfleet-desire-store/internal/errors"
)

// Request size limit. A desire carries one rendered Kubernetes manifest,
// which etcd caps at 1.5 MiB; the limit leaves room for the envelope.
const maxBodyBytes = 2 << 20

// parseContract parses the REST v1 contract that the spec module embeds,
// once per process. The validator only reads it.
var parseContract = sync.OnceValues(func() (*openapi3.T, error) {
	data, err := dsschemas.FS.ReadFile("desire-store/openapi.yaml")
	if err != nil {
		return nil, err
	}
	return openapi3.NewLoader().LoadFromData(data)
})

// validateRequests checks each request against the REST v1 contract before
// next serves it, and answers a violation with an Error Model problem. The
// body is capped at maxBodyBytes first. A request that matches no contract
// operation passes through, so the mux keeps answering unknown routes with
// 404 and wrong methods with 405.
func validateRequests(contract *openapi3.T, log *slog.Logger, next http.Handler) http.Handler {
	validator := nethttpmiddleware.OapiRequestValidatorWithOptions(contract, &nethttpmiddleware.Options{
		Options: openapi3filter.Options{
			// The Wristband middleware authenticates; the contract's bearer
			// scheme is the gateway's.
			AuthenticationFunc: openapi3filter.NoopAuthenticationFunc,
		},
		// The handlers read the body as the client sent it.
		DiscardValidatedValue: true,
		ErrorHandlerWithOpts: func(_ context.Context, err error, w http.ResponseWriter, r *http.Request,
			opts nethttpmiddleware.ErrorHandlerOpts,
		) {
			if opts.MatchedRoute == nil {
				next.ServeHTTP(w, r)
				return
			}
			handleError(w, r, log, validationProblem(err))
		},
	})(next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
		validator.ServeHTTP(w, r)
	})
}

// validationProblem maps a contract violation to an Error Model problem. The
// message comes from the schema rule only: the full validator error text
// carries the schema and the value, which must not reach the client.
func validationProblem(err error) *errors.ServiceError {
	if _, ok := stderrors.AsType[*http.MaxBytesError](err); ok {
		return errors.Invalid(errors.CodeInvalidBody, fieldBody, errors.ConstraintMaxLength,
			fmt.Sprintf("the request body exceeds %d bytes", maxBodyBytes))
	}
	requestErr, ok := stderrors.AsType[*openapi3filter.RequestError](err)
	if !ok {
		return errors.Invalid(errors.CodeInvalidBody, fieldBody, errors.ConstraintFormat,
			"the request does not match the contract").WithCause(err)
	}
	schemaErr, isSchemaErr := stderrors.AsType[*openapi3.SchemaError](requestErr.Err)

	if parameter := requestErr.Parameter; parameter != nil {
		name := parameter.Name
		if stderrors.Is(requestErr.Err, openapi3filter.ErrInvalidRequired) {
			return errors.Invalid(errors.CodeRequired, name, errors.ConstraintRequired, name+" is required").
				WithCause(err)
		}
		code := errors.CodeInvalidQuery
		if parameter.In == openapi3.ParameterInPath {
			code = errors.CodeInvalidPath
		}
		if isSchemaErr {
			return errors.Invalid(code, name, constraint(schemaErr), name+": "+schemaErr.Reason).WithCause(err)
		}
		return errors.Invalid(code, name, errors.ConstraintFormat, name+" has an invalid format").WithCause(err)
	}

	if isSchemaErr {
		field := strings.Join(schemaErr.JSONPointer(), ".")
		switch {
		case field == "":
			return errors.Invalid(errors.CodeInvalidBody, fieldBody, errors.ConstraintFormat,
				"the request body must be a JSON object").WithCause(err)
		case schemaErr.SchemaField == "required":
			return errors.Invalid(errors.CodeRequired, field, errors.ConstraintRequired, field+" is required").
				WithCause(err)
		default:
			return errors.Invalid(errors.CodeInvalidField, field, constraint(schemaErr), field+": "+schemaErr.Reason).
				WithCause(err)
		}
	}
	message := "the request body is not valid JSON"
	switch {
	case stderrors.Is(requestErr.Err, openapi3filter.ErrInvalidRequired):
		message = "the request body is required"
	case requestErr.Err == nil:
		// The Content-Type is not one the operation accepts.
		message = "the request body must be application/json"
	}
	return errors.Invalid(errors.CodeInvalidBody, fieldBody, errors.ConstraintFormat, message).WithCause(err)
}

// constraint names the Error Model constraint of a failed schema rule. A rule
// that none of them describes leaves the constraint out.
func constraint(schemaErr *openapi3.SchemaError) string {
	switch schemaErr.SchemaField {
	case "required":
		return errors.ConstraintRequired
	case "minimum", "exclusiveMinimum":
		return errors.ConstraintMin
	case "maximum", "exclusiveMaximum":
		return errors.ConstraintMax
	case "minLength":
		return errors.ConstraintMinLength
	case "maxLength":
		return errors.ConstraintMaxLength
	case "pattern":
		return errors.ConstraintPattern
	case "enum":
		return errors.ConstraintEnum
	case "type", "nullable", "format":
		return errors.ConstraintFormat
	default:
		return ""
	}
}
