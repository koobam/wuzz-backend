// Package apperr is the one error type every layer speaks: domain, gRPC and
// HTTP all convert to and from it, so a NotFound raised in a command handler
// reaches the client as a 404 without every layer inventing its own mapping.
package apperr

import (
	"errors"
	"net/http"

	"google.golang.org/grpc/codes"
)

type Kind string

const (
	KindInvalid      Kind = "INVALID"
	KindUnauthorized Kind = "UNAUTHORIZED"
	KindForbidden    Kind = "FORBIDDEN"
	KindNotFound     Kind = "NOT_FOUND"
	KindConflict     Kind = "CONFLICT"
	KindRateLimited  Kind = "RATE_LIMITED"
	KindInternal     Kind = "INTERNAL"
)

// Error is the app-wide error value. Code defaults to Kind's string form but
// can be overridden (e.g. from a gRPC ErrorInfo reason) without changing Kind,
// which is what HTTPStatus and gRPC mapping actually switch on.
type Error struct {
	Kind    Kind
	Code    string
	Message string
	Details map[string]string
	cause   error
}

func (e *Error) Error() string {
	if e.cause != nil {
		return e.Message + ": " + e.cause.Error()
	}
	return e.Message
}

func (e *Error) Unwrap() error { return e.cause }

func (e *Error) WithCause(err error) *Error {
	clone := *e
	clone.cause = err
	return &clone
}

func (e *Error) WithCode(code string) *Error {
	clone := *e
	clone.Code = code
	return &clone
}

func (e *Error) WithDetails(details map[string]string) *Error {
	clone := *e
	clone.Details = details
	return &clone
}

func (e *Error) HTTPStatus() int {
	switch e.Kind {
	case KindInvalid:
		return http.StatusBadRequest
	case KindUnauthorized:
		return http.StatusUnauthorized
	case KindForbidden:
		return http.StatusForbidden
	case KindNotFound:
		return http.StatusNotFound
	case KindConflict:
		return http.StatusConflict
	case KindRateLimited:
		return http.StatusTooManyRequests
	default:
		return http.StatusInternalServerError
	}
}

func (e *Error) GRPCCode() codes.Code {
	switch e.Kind {
	case KindInvalid:
		return codes.InvalidArgument
	case KindUnauthorized:
		return codes.Unauthenticated
	case KindForbidden:
		return codes.PermissionDenied
	case KindNotFound:
		return codes.NotFound
	case KindConflict:
		return codes.AlreadyExists
	case KindRateLimited:
		return codes.ResourceExhausted
	default:
		return codes.Internal
	}
}

func newErr(kind Kind, message string) *Error {
	return &Error{Kind: kind, Code: string(kind), Message: message}
}

func Invalid(message string) *Error      { return newErr(KindInvalid, message) }
func Unauthorized(message string) *Error { return newErr(KindUnauthorized, message) }
func Forbidden(message string) *Error    { return newErr(KindForbidden, message) }
func NotFound(message string) *Error     { return newErr(KindNotFound, message) }
func Conflict(message string) *Error     { return newErr(KindConflict, message) }
func RateLimited(message string) *Error  { return newErr(KindRateLimited, message) }
func Internal(message string) *Error     { return newErr(KindInternal, message) }

// From coerces any error into *Error, defaulting to Internal so a bug two
// layers down never leaks its message to a client.
func From(err error) *Error {
	var appErr *Error
	if errors.As(err, &appErr) {
		return appErr
	}
	return Internal("Something went wrong").WithCause(err)
}

// IsKind reports whether err is an *Error of the given kind.
func IsKind(err error, kind Kind) bool {
	var appErr *Error
	return errors.As(err, &appErr) && appErr.Kind == kind
}

// FromGRPCCode maps a gRPC status code back to an app error kind — the
// gateway's side of the boundary GRPCCode draws on the way out.
func FromGRPCCode(code codes.Code, message string) *Error {
	switch code {
	case codes.InvalidArgument:
		return Invalid(message)
	case codes.Unauthenticated:
		return Unauthorized(message)
	case codes.PermissionDenied:
		return Forbidden(message)
	case codes.NotFound:
		return NotFound(message)
	case codes.AlreadyExists:
		return Conflict(message)
	case codes.ResourceExhausted:
		return RateLimited(message)
	default:
		return Internal(message)
	}
}
