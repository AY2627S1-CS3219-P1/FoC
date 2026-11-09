// Client-facing profile picture error vocabulary: neutral contract reasons plus
// the shared google.rpc details that carry them to clients.
package service

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

const (
	reasonInternal                         = "INTERNAL"
	reasonInvalidProfilePicture            = "INVALID_PROFILE_PICTURE"
	reasonProfilePictureNotReady           = "PROFILE_PICTURE_NOT_READY"
	reasonProfilePictureUnavailable        = "PROFILE_PICTURE_UPLOAD_UNAVAILABLE"
	reasonProfilePictureTemporarilyUnavail = "PROFILE_PICTURE_UPLOAD_TEMPORARILY_UNAVAILABLE"
)

const profileErrorDomain = "user.v1"

const (
	profilePictureUnavailableMessage = "Profile picture changes aren't available right now."
	profilePictureTemporarilyMessage = "Profile picture uploads are temporarily unavailable. Try again shortly."
)

var (
	errProfilePictureNotReady           = errors.New("profile picture is not ready")
	errInvalidProfilePicture            = errors.New("profile picture metadata is invalid")
	errProfilePictureUnavailable        = errors.New("profile picture storage is not configured")
	errProfilePictureTemporarilyUnavail = errors.New("profile picture storage is temporarily unavailable")
)

// profileError carries the contract status, reason, curated message and the
// picture a client should point at.
type profileError struct {
	sentinel  error
	status    int
	code      connect.Code
	reason    string
	message   string
	pictureID uuid.UUID
	cause     error
}

func (e *profileError) Error() string                { return e.message }
func (e *profileError) Unwrap() []error              { return []error{e.cause, e.sentinel} }
func (e *profileError) Code() int                    { return e.status }
func (e *profileError) GetConnectCode() connect.Code { return e.code }
func (e *profileError) ErrorTrace() string {
	if e.cause == nil {
		return e.message
	}
	return e.message + "\n" + e.cause.Error()
}
func (e *profileError) PictureID() uuid.UUID { return e.pictureID }

var _ api.ExternalError = (*profileError)(nil)

func profilePictureUnavailable(cause error) *profileError {
	return &profileError{
		sentinel: errProfilePictureUnavailable,
		status:   http.StatusInternalServerError,
		code:     connect.CodeInternal,
		reason:   reasonProfilePictureUnavailable,
		message:  profilePictureUnavailableMessage,
		cause:    cause,
	}
}

func profilePictureTemporarilyUnavailable(cause error) *profileError {
	return &profileError{
		sentinel: errProfilePictureTemporarilyUnavail,
		status:   http.StatusServiceUnavailable,
		code:     connect.CodeUnavailable,
		reason:   reasonProfilePictureTemporarilyUnavail,
		message:  profilePictureTemporarilyMessage,
		cause:    cause,
	}
}

func profilePictureNotReady(cause error, pictureID uuid.UUID, message string) *profileError {
	return &profileError{
		sentinel:  errProfilePictureNotReady,
		status:    http.StatusPreconditionFailed,
		code:      connect.CodeFailedPrecondition,
		reason:    reasonProfilePictureNotReady,
		message:   message,
		pictureID: pictureID,
		cause:     cause,
	}
}

func invalidProfilePicture(message string) *profileError {
	return &profileError{
		sentinel: errInvalidProfilePicture,
		status:   http.StatusBadRequest,
		code:     connect.CodeInvalidArgument,
		reason:   reasonInvalidProfilePicture,
		message:  message,
		cause:    errInvalidProfilePicture,
	}
}

// ProfileConnectError converts profile errors to Connect errors, attaching the
// contract reason and detail for picture failures. Other errors retain the
// shared conversion.
func ProfileConnectError(ctx context.Context, err error) error {
	if err == nil {
		err = errors.New("nil error passed to ProfileConnectError")
	}
	var pictureErr *profileError
	if errors.As(err, &pictureErr) {
		logProfileError(ctx, pictureErr)
		connectErr := connect.NewError(pictureErr.code, errors.New(pictureErr.message))
		attachProfileDetails(ctx, connectErr, pictureErr)
		return connectErr
	}
	var external api.ExternalError
	if errors.As(err, &external) {
		return api.ToConnectError(ctx, err)
	}
	slog.ErrorContext(ctx, "profile operation failed", "err", err)
	connectErr := connect.NewError(connect.CodeInternal, errors.New(api.MsgInternalError))
	attachProfileDetails(ctx, connectErr, &profileError{
		reason: reasonInternal, message: api.MsgInternalError, cause: err,
	})
	return connectErr
}

func attachProfileDetails(ctx context.Context, connectErr *connect.Error, e *profileError) {
	if detail, detailErr := connect.NewErrorDetail(
		&errdetails.ErrorInfo{Domain: profileErrorDomain, Reason: e.reason},
	); detailErr == nil {
		connectErr.AddDetail(detail)
	} else {
		slog.ErrorContext(ctx, "encode profile error info", "err", detailErr)
	}
	detail := &commonv1.ErrorDetail{Reason: e.reason, Message: e.message}
	if e.pictureID != uuid.Nil {
		detail.FieldViolations = append(detail.FieldViolations, &errdetails.BadRequest_FieldViolation{
			Field:       "picture_id",
			Description: e.pictureID.String() + ": " + e.message,
		})
	}
	if encoded, detailErr := connect.NewErrorDetail(detail); detailErr == nil {
		connectErr.AddDetail(encoded)
	} else {
		slog.ErrorContext(ctx, "encode profile error detail", "err", detailErr)
	}
}

func logProfileError(ctx context.Context, e *profileError) {
	err := e.cause
	if err == nil {
		err = e.sentinel
	}
	switch e.reason {
	case reasonProfilePictureTemporarilyUnavail:
		slog.WarnContext(ctx, "profile picture storage transiently unavailable", "err", err)
	case reasonProfilePictureUnavailable:
		if errors.Is(err, errProfilePictureStorageDisabled) {
			slog.WarnContext(ctx, "profile picture uploads are disabled in this deployment", "err", err)
			return
		}
		slog.ErrorContext(ctx, "profile picture storage unavailable", "err", err)
	default:
		slog.ErrorContext(ctx, "profile operation rejected", "err", err)
	}
}
