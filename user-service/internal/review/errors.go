// Client-facing review error vocabulary: neutral contract reasons plus the
// google.rpc details that carry them to clients.
package review

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

// Neutral contract reasons; the real causes stay in server logs.
const (
	reasonInternal                     = "INTERNAL"
	reasonInvalidReview                = "INVALID_REVIEW"
	reasonInvalidPhoto                 = "INVALID_PHOTO"
	reasonPhotoNotStaged               = "PHOTO_NOT_STAGED"
	reasonPhotoNotReady                = "PHOTO_NOT_READY"
	reasonReviewNotFound               = "REVIEW_NOT_FOUND"
	reasonReviewAlreadyExists          = "REVIEW_ALREADY_EXISTS"
	reasonUploadTemporarilyUnavailable = "UPLOAD_TEMPORARILY_UNAVAILABLE"
	reasonUploadUnavailable            = "UPLOAD_UNAVAILABLE"
)

const errorDomain = "user.review.v1"

const (
	// Curated client-facing messages; provider and configuration detail never
	// reaches callers.
	uploadTemporarilyUnavailableMessage = "Photo uploads are temporarily unavailable. Try again, or submit your review without photos."
	uploadUnavailableMessage            = "Photo uploads aren't available. You can still submit your review without photos."
)

var (
	errUploadTemporarilyUnavailable = errors.New("review photo storage is temporarily unavailable")
	errUploadUnavailable            = errors.New("review photo uploads are not available")
	errPhotoNotReady                = errors.New("review photo is not ready to attach")
	errInvalidPhoto                 = errors.New("review photo metadata is invalid")
	errInvalidReview                = errors.New("review input is invalid")
)

// A client-facing review failure: contract status and reason, backend-dictated
// message, and optionally the photo a client should point at.
type uploadError struct {
	sentinel error // mapping key
	status   int
	code     connect.Code
	reason   string
	message  string
	photoID  uuid.UUID // zero when no single photo is at fault
	cause    error
}

func (e *uploadError) Error() string                { return e.message }
func (e *uploadError) Unwrap() []error              { return []error{e.cause, e.sentinel} }
func (e *uploadError) Code() int                    { return e.status }
func (e *uploadError) GetConnectCode() connect.Code { return e.code }
func (e *uploadError) ErrorTrace() string {
	if e.cause == nil {
		return e.message
	}
	return e.message + "\n" + e.cause.Error()
}
func (e *uploadError) PhotoID() uuid.UUID { return e.photoID }

var _ api.ExternalError = (*uploadError)(nil)

func uploadTemporarilyUnavailable(cause error) *uploadError {
	return &uploadError{
		sentinel: errUploadTemporarilyUnavailable,
		status:   http.StatusServiceUnavailable,
		code:     connect.CodeUnavailable,
		reason:   reasonUploadTemporarilyUnavailable,
		message:  uploadTemporarilyUnavailableMessage,
		cause:    cause,
	}
}

func uploadUnavailable(cause error) *uploadError {
	return &uploadError{
		sentinel: errUploadUnavailable,
		status:   http.StatusInternalServerError,
		code:     connect.CodeInternal,
		reason:   reasonUploadUnavailable,
		message:  uploadUnavailableMessage,
		cause:    cause,
	}
}

func photoNotReady(cause error, photoID uuid.UUID, message string) *uploadError {
	return &uploadError{
		sentinel: errPhotoNotReady,
		status:   http.StatusPreconditionFailed,
		code:     connect.CodeFailedPrecondition,
		reason:   reasonPhotoNotReady,
		message:  message,
		photoID:  photoID,
		cause:    cause,
	}
}

func invalidPhoto(message string) *uploadError {
	return &uploadError{
		sentinel: errInvalidPhoto,
		status:   http.StatusBadRequest,
		code:     connect.CodeInvalidArgument,
		reason:   reasonInvalidPhoto,
		message:  message,
		cause:    errInvalidPhoto,
	}
}

type errorMapping struct {
	err    error
	code   connect.Code
	reason string
}

var errorMappings = []errorMapping{
	{errUploadTemporarilyUnavailable, connect.CodeUnavailable, reasonUploadTemporarilyUnavailable},
	{errUploadUnavailable, connect.CodeInternal, reasonUploadUnavailable},
	{errPhotoNotReady, connect.CodeFailedPrecondition, reasonPhotoNotReady},
	{errInvalidPhoto, connect.CodeInvalidArgument, reasonInvalidPhoto},
	{errInvalidReview, connect.CodeInvalidArgument, reasonInvalidReview},
	{errPhotoNotFound, connect.CodeInvalidArgument, reasonPhotoNotStaged},
	{errSelfReview, connect.CodeInvalidArgument, reasonInvalidReview},
	{errReviewerNotFound, connect.CodeInvalidArgument, reasonInvalidReview},
	{errRevieweeNotFound, connect.CodeInvalidArgument, reasonInvalidReview},
	{errReviewAlreadyExists, connect.CodeAlreadyExists, reasonReviewAlreadyExists},
	{errReviewInvalid, connect.CodeInvalidArgument, reasonInvalidReview},
	{errReviewNotFound, connect.CodeNotFound, reasonReviewNotFound},
}

// Mapped reasons carry an ErrorInfo and an ErrorDetail; everything else falls
// through to the shared conversion.
func toConnectError(ctx context.Context, err error) error {
	if err == nil {
		err = errors.New("nil error passed to toConnectError")
	}
	for _, m := range errorMappings {
		if !errors.Is(err, m.err) {
			continue
		}
		return mappedError(ctx, err, m)
	}
	var extErr api.ExternalError
	if errors.As(err, &extErr) {
		return api.ToConnectError(ctx, err)
	}
	// Unknown failure: generic message, but still tagged with the neutral
	// reason so clients handle every 500 through the same path.
	slog.ErrorContext(ctx, "review operation failed", "err", err)
	connectErr := connect.NewError(connect.CodeInternal, errors.New(api.MsgInternalError))
	attachDetails(ctx, connectErr, reasonInternal, err)
	return connectErr
}

func mappedError(ctx context.Context, err error, m errorMapping) error {
	logMappedError(ctx, err, m.reason)
	connectErr := connect.NewError(m.code, errors.New(clientMessage(err, m.reason)))
	attachDetails(ctx, connectErr, m.reason, err)
	return connectErr
}

// clientMessage returns the backend-dictated user-facing text: curated for
// availability and unknown failures, the domain message otherwise.
func clientMessage(err error, reason string) string {
	switch reason {
	case reasonUploadTemporarilyUnavailable:
		return uploadTemporarilyUnavailableMessage
	case reasonUploadUnavailable:
		return uploadUnavailableMessage
	case reasonInternal:
		// The raw failure text is internal detail; callers get the generic.
		return api.MsgInternalError
	}
	return err.Error()
}

// ErrorDetail bundles message and violations in one descriptor so TypeScript
// clients decode a single type.
func attachDetails(ctx context.Context, connectErr *connect.Error, reason string, err error) {
	if detail, detailErr := connect.NewErrorDetail(
		&errdetails.ErrorInfo{Domain: errorDomain, Reason: reason},
	); detailErr == nil {
		connectErr.AddDetail(detail)
	} else {
		slog.ErrorContext(ctx, "encode review error info", "err", detailErr)
	}
	detail := &commonv1.ErrorDetail{Reason: reason, Message: clientMessage(err, reason)}
	var notReady *uploadError
	if errors.As(err, &notReady) && notReady.photoID != uuid.Nil {
		detail.FieldViolations = append(detail.FieldViolations, &errdetails.BadRequest_FieldViolation{
			Field:       "photo_ids",
			Description: notReady.photoID.String() + ": " + notReady.message,
		})
	}
	if encoded, detailErr := connect.NewErrorDetail(detail); detailErr == nil {
		connectErr.AddDetail(encoded)
	} else {
		slog.ErrorContext(ctx, "encode review error detail", "err", detailErr)
	}
}

// logMappedError keeps provider and configuration detail in the logs: clients
// only ever see the neutral reason and curated message.
func logMappedError(ctx context.Context, err error, reason string) {
	// The surface error is the curated client message; the wrapped cause
	// carries what operations actually need.
	var ue *uploadError
	if errors.As(err, &ue) && ue.cause != nil {
		err = ue.cause
	}
	switch reason {
	case reasonUploadTemporarilyUnavailable:
		slog.WarnContext(ctx, "review photo storage transiently unavailable", "err", err)
	case reasonUploadUnavailable:
		if errors.Is(err, errPhotoStorageDisabled) {
			slog.WarnContext(ctx, "review photo uploads are disabled in this deployment", "err", err)
			return
		}
		slog.ErrorContext(ctx, "review photo storage unavailable", "err", err)
	default:
		slog.ErrorContext(ctx, "review operation rejected", "err", err)
	}
}
