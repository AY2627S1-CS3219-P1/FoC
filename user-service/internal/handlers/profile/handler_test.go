package profile

import (
	"context"
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth"
	userv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/images"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/models"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/service"
	"github.com/google/uuid"
)

type profileLogicStub struct {
	view         service.ProfileView
	getErr       error
	updateErr    error
	pictureErr   error
	removeErr    error
	upload       service.ProfilePictureUpload
	grant        images.UploadGrant
	uploadErr    error
	updatedWith  uuid.UUID
	removedActor uuid.UUID
}

func (s *profileLogicStub) GetMyProfile(context.Context, uuid.UUID) (service.ProfileView, error) {
	return s.view, s.getErr
}

func (s *profileLogicStub) UpdateMyProfile(
	context.Context,
	uuid.UUID,
	service.ProfileInput,
) (service.ProfileView, error) {
	return s.view, s.updateErr
}

func (s *profileLogicStub) CreateProfilePictureUpload(
	context.Context,
	uuid.UUID,
	string,
	int64,
) (service.ProfilePictureUpload, images.UploadGrant, error) {
	return s.upload, s.grant, s.uploadErr
}

func (s *profileLogicStub) UpdateMyProfilePicture(
	_ context.Context,
	actorID, pictureID uuid.UUID,
) (service.ProfileView, error) {
	s.updatedWith = pictureID
	return s.view, s.pictureErr
}

func (s *profileLogicStub) RemoveMyProfilePicture(
	_ context.Context,
	actorID uuid.UUID,
) (service.ProfileView, error) {
	s.removedActor = actorID
	return s.view, s.removeErr
}

func TestGetMyProfileMapsPictureFields(t *testing.T) {
	actorID, pictureID := uuid.New(), uuid.New()
	expiresAt := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	logic := &profileLogicStub{view: service.ProfileView{
		User: models.User{ID: actorID, Email: "casey@example.com", DisplayName: "Casey",
			ProfilePictureID: &pictureID},
		PictureURL:       "https://storage.test/get",
		PictureExpiresAt: expiresAt,
	}}
	ctx := auth.WithCaller(context.Background(), auth.Caller{ID: actorID.String()})

	response, err := (&Handler{Logic: logic}).GetMyProfile(
		ctx, connect.NewRequest(&userv1.GetMyProfileRequest{}),
	)
	if err != nil {
		t.Fatal(err)
	}
	profile := response.Msg.GetProfile()
	if profile.GetProfilePictureId() != pictureID.String() ||
		profile.GetProfilePictureUrl() != "https://storage.test/get" ||
		profile.GetProfilePictureExpiresAt() == nil ||
		!profile.GetProfilePictureExpiresAt().AsTime().Equal(expiresAt) {
		t.Fatalf("profile picture mapping = %+v", profile)
	}
	if got := response.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q", got)
	}
}

func TestCreateProfilePictureUploadMapsGrant(t *testing.T) {
	actorID, pictureID := uuid.New(), uuid.New()
	expiresAt := time.Date(2026, 11, 8, 12, 10, 0, 0, time.UTC)
	logic := &profileLogicStub{
		upload: service.ProfilePictureUpload{ID: pictureID},
		grant: images.UploadGrant{
			URL: "https://storage.test/put", Method: "PUT",
			Headers: map[string]string{"Content-Type": "image/png"}, ExpiresAt: expiresAt,
		},
	}
	ctx := auth.WithCaller(context.Background(), auth.Caller{ID: actorID.String()})

	response, err := (&Handler{Logic: logic}).CreateProfilePictureUpload(
		ctx,
		connect.NewRequest(&userv1.CreateProfilePictureUploadRequest{
			ContentType: "image/png", SizeBytes: 4,
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	msg := response.Msg
	if msg.GetPictureId() != pictureID.String() ||
		msg.GetUploadUrl() != "https://storage.test/put" ||
		msg.GetUploadHeaders()["Content-Type"] != "image/png" ||
		msg.GetExpiresAt() == nil || !msg.GetExpiresAt().AsTime().Equal(expiresAt) {
		t.Fatalf("grant mapping = %+v", msg)
	}
	if got := response.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q", got)
	}
}

func TestUpdateMyProfilePictureMapsCallerAndPicture(t *testing.T) {
	actorID, pictureID := uuid.New(), uuid.New()
	logic := &profileLogicStub{view: service.ProfileView{
		User: models.User{ID: actorID, Email: "casey@example.com", DisplayName: "Casey"},
	}}
	ctx := auth.WithCaller(context.Background(), auth.Caller{ID: actorID.String()})

	response, err := (&Handler{Logic: logic}).UpdateMyProfilePicture(
		ctx,
		connect.NewRequest(&userv1.UpdateMyProfilePictureRequest{PictureId: pictureID.String()}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if logic.updatedWith != pictureID || response.Msg.GetProfile().GetId() != actorID.String() {
		t.Fatalf("update mapping: picture=%s profile=%+v", logic.updatedWith, response.Msg.GetProfile())
	}
}

func TestUpdateMyProfilePictureRejectsMalformedID(t *testing.T) {
	actorID := uuid.New()
	ctx := auth.WithCaller(context.Background(), auth.Caller{ID: actorID.String()})
	_, err := (&Handler{Logic: &profileLogicStub{}}).UpdateMyProfilePicture(
		ctx,
		connect.NewRequest(&userv1.UpdateMyProfilePictureRequest{PictureId: "not-a-uuid"}),
	)
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("malformed picture id: %v", err)
	}
}

func TestRemoveMyProfilePictureUsesCaller(t *testing.T) {
	actorID := uuid.New()
	logic := &profileLogicStub{view: service.ProfileView{
		User: models.User{ID: actorID, Email: "casey@example.com", DisplayName: "Casey"},
	}}
	ctx := auth.WithCaller(context.Background(), auth.Caller{ID: actorID.String()})

	if _, err := (&Handler{Logic: logic}).RemoveMyProfilePicture(
		ctx, connect.NewRequest(&userv1.RemoveMyProfilePictureRequest{}),
	); err != nil {
		t.Fatal(err)
	}
	if logic.removedActor != actorID {
		t.Fatalf("removed actor = %s", logic.removedActor)
	}
}

func TestProfilePictureErrorMapping(t *testing.T) {
	actorID := uuid.New()
	ctx := auth.WithCaller(context.Background(), auth.Caller{ID: actorID.String()})
	logic := &profileLogicStub{getErr: errors.New("boom")}
	_, err := (&Handler{Logic: logic}).GetMyProfile(
		ctx, connect.NewRequest(&userv1.GetMyProfileRequest{}),
	)
	if connect.CodeOf(err) != connect.CodeInternal {
		t.Fatalf("internal mapping = %v", err)
	}
}
