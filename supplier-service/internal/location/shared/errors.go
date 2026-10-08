package shared

import "errors"

var (
	ErrInvalidArgument    = errors.New("invalid argument")
	ErrUnauthenticated    = errors.New("authentication required")
	ErrAccessDenied       = errors.New("permission denied")
	ErrResourceNotFound   = errors.New("resource not found")
	ErrFailedPrecondition = errors.New("invalid state")
	ErrAlreadyExists      = errors.New("conflict")
	ErrAborted            = errors.New("stale revision")
)
