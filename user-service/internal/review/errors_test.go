package review

import (
	"errors"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api/errs"
	commonv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/common/v1"
	"github.com/google/uuid"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/protobuf/proto"
)

var errStorageMisconfiguredStub = errors.New("AccessDenied")

func decodedDetails(t *testing.T, err error) []proto.Message {
	t.Helper()
	connectErr, ok := err.(*connect.Error)
	if !ok {
		t.Fatalf("want *connect.Error, got %T", err)
	}
	var out []proto.Message
	for _, detail := range connectErr.Details() {
		value, valueErr := detail.Value()
		if valueErr != nil {
			t.Fatalf("decode error detail: %v", valueErr)
		}
		out = append(out, value)
	}
	return out
}

func detailOf(t *testing.T, err error) *commonv1.ErrorDetail {
	t.Helper()
	for _, value := range decodedDetails(t, err) {
		if decoded, ok := value.(*commonv1.ErrorDetail); ok {
			return decoded
		}
	}
	t.Fatal("no common.v1.ErrorDetail attached")
	return nil
}

func reasonOf(t *testing.T, err error) string {
	t.Helper()
	for _, value := range decodedDetails(t, err) {
		if info, ok := value.(*errdetails.ErrorInfo); ok && info.Domain == errorDomain {
			return info.Reason
		}
	}
	t.Fatal("no user.review.v1 ErrorInfo attached")
	return ""
}

func TestToConnectErrorMapsReasons(t *testing.T) {
	offender := uuid.New()
	cases := []struct {
		name    string
		err     error
		code    connect.Code
		reason  string
		message string
	}{
		{
			name:    "transient storage fault is 503 with the curated message",
			err:     uploadTemporarilyUnavailable(errors.New("dial tcp: refused")),
			code:    connect.CodeUnavailable,
			reason:  reasonUploadTemporarilyUnavailable,
			message: uploadTemporarilyUnavailableMessage,
		},
		{
			name:    "disabled storage is 500 with the curated message",
			err:     uploadUnavailable(errPhotoStorageDisabled),
			code:    connect.CodeInternal,
			reason:  reasonUploadUnavailable,
			message: uploadUnavailableMessage,
		},
		{
			name:    "misconfigured storage is 500 with the curated message",
			err:     uploadUnavailable(errStorageMisconfiguredStub),
			code:    connect.CodeInternal,
			reason:  reasonUploadUnavailable,
			message: uploadUnavailableMessage,
		},
		{
			name:    "not-ready photo is 412",
			err:     photoNotReady(errPhotoNotReady, offender, "photo upload is missing; upload it again"),
			code:    connect.CodeFailedPrecondition,
			reason:  reasonPhotoNotReady,
			message: "photo upload is missing; upload it again",
		},
		{
			name:    "not-staged photo is 400",
			err:     errs.WrapBadRequestError(errPhotoNotFound, "photo was not staged by this user"),
			code:    connect.CodeInvalidArgument,
			reason:  reasonPhotoNotStaged,
			message: "photo was not staged by this user",
		},
		{
			name:    "invalid photo metadata is 400",
			err:     invalidPhoto("photo size must be between 1 and 10000000 bytes"),
			code:    connect.CodeInvalidArgument,
			reason:  reasonInvalidPhoto,
			message: "photo size must be between 1 and 10000000 bytes",
		},
		{
			name:    "self review is 400",
			err:     errs.WrapBadRequestError(errSelfReview, "cannot review yourself"),
			code:    connect.CodeInvalidArgument,
			reason:  reasonInvalidReview,
			message: "cannot review yourself",
		},
		{
			name:    "duplicate review is 409",
			err:     errs.WrapAlreadyExistsError(errReviewAlreadyExists, "review already exists"),
			code:    connect.CodeAlreadyExists,
			reason:  reasonReviewAlreadyExists,
			message: "review already exists",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := toConnectError(t.Context(), tc.err)
			if connect.CodeOf(err) != tc.code {
				t.Fatalf("code = %v, want %v", connect.CodeOf(err), tc.code)
			}
			if got := reasonOf(t, err); got != tc.reason {
				t.Fatalf("reason = %q, want %q", got, tc.reason)
			}
			detail := detailOf(t, err)
			if detail.Reason != tc.reason {
				t.Fatalf("detail reason = %q, want %q", detail.Reason, tc.reason)
			}
			if detail.Message != tc.message {
				t.Fatalf("detail message = %q, want %q", detail.Message, tc.message)
			}
			lowered := strings.ToLower(detail.Message)
			for _, leaked := range []string{"storage", "s3", "presign", "bucket", "misconfigured", "disabled"} {
				if strings.Contains(lowered, leaked) {
					t.Fatalf("message leaks implementation detail %q: %q", leaked, detail.Message)
				}
			}
		})
	}
}

func TestToConnectErrorAttachesPhotoViolation(t *testing.T) {
	offender := uuid.New()
	err := toConnectError(
		t.Context(),
		photoNotReady(errPhotoNotReady, offender, "photo upload is missing; upload it again"),
	)
	detail := detailOf(t, err)
	if len(detail.FieldViolations) != 1 {
		t.Fatalf("want one field violation, got %d", len(detail.FieldViolations))
	}
	violation := detail.FieldViolations[0]
	if violation.Field != "photo_ids" {
		t.Fatalf("violation field = %q, want photo_ids", violation.Field)
	}
	if !strings.HasPrefix(violation.Description, offender.String()) {
		t.Fatalf("violation description = %q, want the offending photo id prefix", violation.Description)
	}
}

func TestToConnectErrorWithoutPhotoHasNoViolation(t *testing.T) {
	detail := detailOf(t, toConnectError(t.Context(), uploadUnavailable(errPhotoStorageDisabled)))
	if len(detail.FieldViolations) != 0 {
		t.Fatalf("want no field violations, got %d", len(detail.FieldViolations))
	}
}

func TestToConnectErrorFallsBackToSharedConversion(t *testing.T) {
	err := toConnectError(t.Context(), errs.NewBadRequestError("invalid pagination"))
	if connect.CodeOf(err) != connect.CodeInvalidArgument || !strings.HasSuffix(err.Error(), "invalid pagination") {
		t.Fatalf("unmapped client error changed: %v", err)
	}
	err = toConnectError(t.Context(), errBoomTest)
	if connect.CodeOf(err) != connect.CodeInternal || !strings.HasSuffix(err.Error(), api.MsgInternalError) {
		t.Fatalf("unknown error should be a generic internal failure, got: %v", err)
	}
	if got := reasonOf(t, err); got != reasonInternal {
		t.Fatalf("unknown error reason = %q, want %q", got, reasonInternal)
	}
	detail := detailOf(t, err)
	if detail.Reason != reasonInternal || detail.Message != api.MsgInternalError {
		t.Fatalf("unknown error detail = %+v", detail)
	}
}
