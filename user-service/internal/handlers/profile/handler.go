package profile

import (
	"context"

	"connectrpc.com/connect"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api/errs"
	userv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/v1/userv1connect"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/images"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/handlers/userdto"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/service"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type Logic interface {
	GetMyProfile(context.Context, uuid.UUID) (service.ProfileView, error)
	UpdateMyProfile(context.Context, uuid.UUID, service.ProfileInput) (service.ProfileView, error)
	CreateProfilePictureUpload(
		context.Context,
		uuid.UUID,
		string,
		int64,
	) (service.ProfilePictureUpload, images.UploadGrant, error)
	UpdateMyProfilePicture(context.Context, uuid.UUID, uuid.UUID) (service.ProfileView, error)
	RemoveMyProfilePicture(context.Context, uuid.UUID) (service.ProfileView, error)
}

type Handler struct {
	userv1connect.UnimplementedProfileServiceHandler
	Logic Logic
}

func (h *Handler) GetMyProfile(
	ctx context.Context,
	_ *connect.Request[userv1.GetMyProfileRequest],
) (*connect.Response[userv1.GetMyProfileResponse], error) {
	actorID, err := userdto.ActorID(ctx)
	if err != nil {
		return nil, api.ToConnectError(ctx, err)
	}
	profile, err := h.Logic.GetMyProfile(ctx, actorID)
	if err != nil {
		return nil, api.ToConnectError(ctx, err)
	}
	response := connect.NewResponse(&userv1.GetMyProfileResponse{Profile: toProtoProfile(profile)})
	response.Header().Set("Cache-Control", "no-store")
	return response, nil
}

func (h *Handler) UpdateMyProfile(
	ctx context.Context,
	req *connect.Request[userv1.UpdateMyProfileRequest],
) (*connect.Response[userv1.UpdateMyProfileResponse], error) {
	actorID, err := userdto.ActorID(ctx)
	if err != nil {
		return nil, api.ToConnectError(ctx, err)
	}
	profile, err := h.Logic.UpdateMyProfile(ctx, actorID, service.ProfileInput{
		DisplayName: req.Msg.DisplayName, Description: req.Msg.Description,
		PhoneNumber: req.Msg.PhoneNumber,
	})
	if err != nil {
		return nil, api.ToConnectError(ctx, err)
	}
	response := connect.NewResponse(&userv1.UpdateMyProfileResponse{Profile: toProtoProfile(profile)})
	response.Header().Set("Cache-Control", "no-store")
	return response, nil
}

// CreateProfilePictureUpload grants one pre-signed PUT; the browser uploads
// the bytes directly to storage, so the response carries the grant untouched.
func (h *Handler) CreateProfilePictureUpload(
	ctx context.Context,
	req *connect.Request[userv1.CreateProfilePictureUploadRequest],
) (*connect.Response[userv1.CreateProfilePictureUploadResponse], error) {
	actorID, err := userdto.ActorID(ctx)
	if err != nil {
		return nil, api.ToConnectError(ctx, err)
	}
	upload, grant, err := h.Logic.CreateProfilePictureUpload(
		ctx, actorID, req.Msg.GetContentType(), req.Msg.GetSizeBytes(),
	)
	if err != nil {
		return nil, api.ToConnectError(ctx, err)
	}
	return connect.NewResponse(&userv1.CreateProfilePictureUploadResponse{
		PictureId:     upload.ID.String(),
		UploadUrl:     grant.URL,
		UploadHeaders: grant.Headers,
		ExpiresAt:     timestamppb.New(grant.ExpiresAt),
	}), nil
}

// UpdateMyProfilePicture validates and applies the pending upload in one call.
func (h *Handler) UpdateMyProfilePicture(
	ctx context.Context,
	req *connect.Request[userv1.UpdateMyProfilePictureRequest],
) (*connect.Response[userv1.UpdateMyProfilePictureResponse], error) {
	actorID, err := userdto.ActorID(ctx)
	if err != nil {
		return nil, api.ToConnectError(ctx, err)
	}
	pictureID, err := uuid.Parse(req.Msg.GetPictureId())
	if err != nil || pictureID == uuid.Nil {
		return nil, api.ToConnectError(ctx, errs.NewBadRequestError("invalid picture ID"))
	}
	profile, err := h.Logic.UpdateMyProfilePicture(ctx, actorID, pictureID)
	if err != nil {
		return nil, api.ToConnectError(ctx, err)
	}
	response := connect.NewResponse(&userv1.UpdateMyProfilePictureResponse{Profile: toProtoProfile(profile)})
	response.Header().Set("Cache-Control", "no-store")
	return response, nil
}

// RemoveMyProfilePicture clears the current picture. Repeating it is safe.
func (h *Handler) RemoveMyProfilePicture(
	ctx context.Context,
	_ *connect.Request[userv1.RemoveMyProfilePictureRequest],
) (*connect.Response[userv1.RemoveMyProfilePictureResponse], error) {
	actorID, err := userdto.ActorID(ctx)
	if err != nil {
		return nil, api.ToConnectError(ctx, err)
	}
	profile, err := h.Logic.RemoveMyProfilePicture(ctx, actorID)
	if err != nil {
		return nil, api.ToConnectError(ctx, err)
	}
	response := connect.NewResponse(&userv1.RemoveMyProfilePictureResponse{Profile: toProtoProfile(profile)})
	response.Header().Set("Cache-Control", "no-store")
	return response, nil
}

func toProtoProfile(profile service.ProfileView) *userv1.Profile {
	return userdto.ProfileWithPicture(profile.User, profile.PictureURL, profile.PictureExpiresAt)
}
