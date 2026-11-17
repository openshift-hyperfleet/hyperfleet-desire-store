// Package errors defines ServiceError, the one error type that the service and
// handler layers return, and its RFC 9457 problem form. The codes, types, and
// constraints come from the HyperFleet Error Model standard.
package errors

import (
	"net/http"
	"strconv"
)

// TypeBase prefixes every problem type URI.
const TypeBase = "https://api.hyperfleet.io/errors/"

// Error codes from the Error Model standard.
const (
	CodeRequired          = "HYPERFLEET-VAL-001"
	CodeInvalidField      = "HYPERFLEET-VAL-002"
	CodeInvalidBody       = "HYPERFLEET-VAL-003"
	CodeInvalidQuery      = "HYPERFLEET-VAL-004"
	CodeInvalidPath       = "HYPERFLEET-VAL-005"
	CodePermissionDenied  = "HYPERFLEET-AUZ-001"
	CodePartitionDenied   = "HYPERFLEET-AUZ-002"
	CodeEndpointNotFound  = "HYPERFLEET-NTF-000"
	CodeNotFound          = "HYPERFLEET-NTF-001"
	CodeAlreadyExists     = "HYPERFLEET-CNF-001"
	CodeVersionConflict   = "HYPERFLEET-CNF-002"
	CodeDeletePending     = "HYPERFLEET-CNF-003"
	CodeOwnerConflict     = "HYPERFLEET-CNF-004"
	CodeUnavailable       = "HYPERFLEET-SVC-001"
	CodeDependencyFailure = "HYPERFLEET-SVC-002"
)

// Validation constraints from the Error Model standard. A violation that none
// of them describes, such as a field the request must not carry, leaves the
// constraint out.
const (
	ConstraintRequired  = "required"
	ConstraintMin       = "min"
	ConstraintMax       = "max"
	ConstraintMinLength = "min_length"
	ConstraintMaxLength = "max_length"
	ConstraintPattern   = "pattern"
	ConstraintEnum      = "enum"
	ConstraintFormat    = "format"
)

// ProblemType is a problem type of the Error Model standard and its title.
type ProblemType struct{ Slug, Title string }

// Problem types from the Error Model standard.
var (
	TypeValidation         = ProblemType{"validation-error", "Validation Error"}
	TypeInvalidRequest     = ProblemType{"invalid-request", "Invalid Request"}
	TypeAuthRequired       = ProblemType{"authentication-required", "Authentication Required"}
	TypeInvalidCredentials = ProblemType{"invalid-credentials", "Invalid Credentials"}
	TypePermissionDenied   = ProblemType{"permission-denied", "Permission Denied"}
	TypeNotFound           = ProblemType{"resource-not-found", "Resource Not Found"}
	TypeEndpointNotFound   = ProblemType{"resource-not-found", "Endpoint Not Found"}
	TypeResourceConflict   = ProblemType{"resource-conflict", "Resource Conflict"}
	TypeVersionConflict    = ProblemType{"version-conflict", "Version Conflict"}
	TypeUnavailable        = ProblemType{"service-unavailable", "Service Unavailable"}
)

// ValidationDetail is one entry of a problem's errors array.
type ValidationDetail struct {
	Field      string `json:"field"`
	Constraint string `json:"constraint,omitempty"`
	Message    string `json:"message"`
}

// ServiceError is a failed request. Reason and Details go to the client and
// carry no internal error text; Cause, set with WithCause, goes only to the
// log.
type ServiceError struct {
	HTTPCode        int
	Type            ProblemType
	Code            string
	Reason          string
	Details         []ValidationDetail
	ExpectedVersion *int64
	ActualVersion   *int64
	Cause           error
}

func (e *ServiceError) Error() string { return e.Code + ": " + e.Reason }

// Problem is the RFC 9457 wire form of a ServiceError.
type Problem struct {
	Type            string             `json:"type"`
	Title           string             `json:"title"`
	Status          int                `json:"status"`
	Detail          string             `json:"detail,omitempty"`
	Instance        string             `json:"instance"`
	Code            string             `json:"code,omitempty"`
	Timestamp       string             `json:"timestamp"`
	TraceID         string             `json:"trace_id,omitempty"`
	Errors          []ValidationDetail `json:"errors,omitempty"`
	ExpectedVersion *int64             `json:"expected_version,omitempty"`
	ActualVersion   *int64             `json:"actual_version,omitempty"`
}

// AsProblem returns the problem body for the request path, timestamp, and
// trace ID.
func (e *ServiceError) AsProblem(instance, timestamp, traceID string) Problem {
	return Problem{
		Type:            TypeBase + e.Type.Slug,
		Title:           e.Type.Title,
		Status:          e.HTTPCode,
		Detail:          e.Reason,
		Instance:        instance,
		Code:            e.Code,
		Timestamp:       timestamp,
		TraceID:         traceID,
		Errors:          e.Details,
		ExpectedVersion: e.ExpectedVersion,
		ActualVersion:   e.ActualVersion,
	}
}

// Invalid is a 400 for one field. A malformed body is an invalid request;
// every other code is a field validation failure.
func Invalid(code, field, constraint, message string) *ServiceError {
	problemType := TypeValidation
	if code == CodeInvalidBody {
		problemType = TypeInvalidRequest
	}
	return forField(http.StatusBadRequest, problemType, code, field, constraint, message)
}

// Unprocessable is a 422 for one field: the body is well formed but
// contradicts the request.
func Unprocessable(field, constraint, message string) *ServiceError {
	return forField(http.StatusUnprocessableEntity, TypeValidation, CodeInvalidField, field, constraint, message)
}

func forField(status int, problemType ProblemType, code, field, constraint, message string) *ServiceError {
	return &ServiceError{
		HTTPCode: status, Type: problemType, Code: code, Reason: message,
		Details: []ValidationDetail{{Field: field, Constraint: constraint, Message: message}},
	}
}

// Unauthenticated is a 401. missing tells an absent credential from an
// invalid one.
func Unauthenticated(code, reason string, missing bool) *ServiceError {
	problemType := TypeInvalidCredentials
	if missing {
		problemType = TypeAuthRequired
	}
	return &ServiceError{HTTPCode: http.StatusUnauthorized, Type: problemType, Code: code, Reason: reason}
}

// Forbidden is a 403.
func Forbidden(code, reason string) *ServiceError {
	return &ServiceError{HTTPCode: http.StatusForbidden, Type: TypePermissionDenied, Code: code, Reason: reason}
}

// NotFound is a 404 for a stored resource.
func NotFound(reason string) *ServiceError {
	return &ServiceError{HTTPCode: http.StatusNotFound, Type: TypeNotFound, Code: CodeNotFound, Reason: reason}
}

// EndpointNotFound is a 404 for a path that matches no route.
func EndpointNotFound() *ServiceError {
	return &ServiceError{
		HTTPCode: http.StatusNotFound, Type: TypeEndpointNotFound, Code: CodeEndpointNotFound,
		Reason: "The requested endpoint does not exist.",
	}
}

// Conflict is a 409 resource conflict.
func Conflict(code, reason string) *ServiceError {
	return &ServiceError{HTTPCode: http.StatusConflict, Type: TypeResourceConflict, Code: code, Reason: reason}
}

// VersionConflict is a 409 for a compare-and-swap version that did not
// match. actual is nil when the stored version could not be read.
func VersionConflict(expected int64, actual *int64) *ServiceError {
	reason := "Desire was modified by another request. Expected version " + strconv.FormatInt(expected, 10) + "."
	if actual != nil {
		reason = "Desire was modified by another request. Expected version " + strconv.FormatInt(expected, 10) +
			", found version " + strconv.FormatInt(*actual, 10) + "."
	}
	return &ServiceError{
		HTTPCode: http.StatusConflict, Type: TypeVersionConflict, Code: CodeVersionConflict, Reason: reason,
		ExpectedVersion: &expected, ActualVersion: actual,
	}
}

// Unavailable is a 503.
func Unavailable(code, reason string) *ServiceError {
	return &ServiceError{HTTPCode: http.StatusServiceUnavailable, Type: TypeUnavailable, Code: code, Reason: reason}
}

// WithCause sets the error that caused e and returns e.
func (e *ServiceError) WithCause(cause error) *ServiceError {
	e.Cause = cause
	return e
}
