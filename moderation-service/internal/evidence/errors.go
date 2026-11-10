package evidence

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"connectrpc.com/connect"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api"
	commonv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/common/v1"
	"github.com/google/uuid"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
)

// Contract reasons, shared with review photos so clients handle them alike.
const (
	ReasonInvalidEvidence              = "INVALID_EVIDENCE"
	ReasonEvidenceNotStaged            = "EVIDENCE_NOT_STAGED"
	ReasonEvidenceNotReady             = "EVIDENCE_NOT_READY"
	ReasonUploadTemporarilyUnavailable = "UPLOAD_TEMPORARILY_UNAVAILABLE"
	ReasonUploadUnavailable            = "UPLOAD_UNAVAILABLE"
)

const errorDomain = "user.evidence.v1"

const (
	// Curated messages; provider and configuration detail never reaches callers.
	temporarilyUnavailableMessage = "Image uploads are temporarily unavailable. Try again, or submit without images."
	unavailableMessage            = "Image uploads aren't available. You can still submit without images."
)

// Error is a client-facing evidence failure. EvidenceID names the image at
// fault, when there is one, so clients can mark it.
type Error struct {
	sentinel   error
	status     int
	code       connect.Code
	reason     string
	message    string
	evidenceID uuid.UUID
	cause      error
}

var _ api.ExternalError = (*Error)(nil)

func (e *Error) Error() string                { return e.message }
func (e *Error) Unwrap() []error              { return []error{e.cause, e.sentinel} }
func (e *Error) Code() int                    { return e.status }
func (e *Error) GetConnectCode() connect.Code { return e.code }
func (e *Error) Reason() string               { return e.reason }
func (e *Error) ErrorTrace() string {
	if e.cause == nil {
		return e.message
	}
	return e.message + "\n" + e.cause.Error()
}

func invalid(sentinel error, message string) *Error {
	return &Error{
		sentinel: sentinel,
		status:   http.StatusBadRequest,
		code:     connect.CodeInvalidArgument,
		reason:   ReasonInvalidEvidence,
		message:  message,
		cause:    sentinel,
	}
}

func notStaged(cause error, evidenceID uuid.UUID) *Error {
	return &Error{
		sentinel:   errEvidenceNotStaged,
		status:     http.StatusBadRequest,
		code:       connect.CodeInvalidArgument,
		reason:     ReasonEvidenceNotStaged,
		message:    "image was not staged by this user",
		evidenceID: evidenceID,
		cause:      cause,
	}
}

func notReady(evidenceID uuid.UUID, message string) *Error {
	return &Error{
		sentinel:   errEvidenceNotReady,
		status:     http.StatusPreconditionFailed,
		code:       connect.CodeFailedPrecondition,
		reason:     ReasonEvidenceNotReady,
		message:    message,
		evidenceID: evidenceID,
		cause:      errEvidenceNotReady,
	}
}

func temporarilyUnavailable(cause error) *Error {
	return &Error{
		sentinel: errUploadTemporarily,
		status:   http.StatusServiceUnavailable,
		code:     connect.CodeUnavailable,
		reason:   ReasonUploadTemporarilyUnavailable,
		message:  temporarilyUnavailableMessage,
		cause:    cause,
	}
}

// unavailable is reported as an internal error; the message stays curated.
func unavailable(cause error) *Error {
	return &Error{
		sentinel: errUploadUnavailable,
		status:   http.StatusInternalServerError,
		code:     connect.CodeInternal,
		reason:   ReasonUploadUnavailable,
		message:  unavailableMessage,
		cause:    cause,
	}
}

// ToConnectError converts evidence failures with their reason and the image at
// fault, using google.rpc.ErrorInfo and common.v1.ErrorDetail like review
// photos. Other errors use the shared conversion.
func ToConnectError(ctx context.Context, err error) error {
	var evidenceErr *Error
	if !errors.As(err, &evidenceErr) {
		return api.ToConnectError(ctx, err)
	}

	slog.WarnContext(ctx, "evidence request rejected", "reason", evidenceErr.reason, "err", evidenceErr.ErrorTrace())
	connectErr := connect.NewError(evidenceErr.code, errors.New(evidenceErr.message))
	if info, detailErr := connect.NewErrorDetail(
		&errdetails.ErrorInfo{Domain: errorDomain, Reason: evidenceErr.reason},
	); detailErr == nil {
		connectErr.AddDetail(info)
	}

	detail := &commonv1.ErrorDetail{Reason: evidenceErr.reason, Message: evidenceErr.message}
	if evidenceErr.evidenceID != uuid.Nil {
		detail.FieldViolations = append(detail.FieldViolations, &errdetails.BadRequest_FieldViolation{
			Field:       "evidence_ids",
			Description: evidenceErr.evidenceID.String() + ": " + evidenceErr.message,
		})
	}
	if encoded, detailErr := connect.NewErrorDetail(detail); detailErr == nil {
		connectErr.AddDetail(encoded)
	}
	return connectErr
}
