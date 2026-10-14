package admin

import (
	"context"

	"connectrpc.com/connect"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api/errs"
	userv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/v1/userv1connect"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/handlers/userdto"
	"github.com/google/uuid"
)

type AllowedDomainLogic interface {
	ListAllowedDomains(context.Context) ([]AllowedDomain, error)
	AllowDomain(context.Context, string, uuid.UUID) error
	RemoveAllowedDomain(context.Context, uuid.UUID) error
}

type AllowedDomainHandler struct {
	userv1connect.UnimplementedAllowedDomainAdminServiceHandler
	Logic AllowedDomainLogic
}

var _ userv1connect.AllowedDomainAdminServiceHandler = (*AllowedDomainHandler)(nil)

func (h *AllowedDomainHandler) ListAllowedDomains(ctx context.Context, _ *connect.Request[userv1.ListAllowedDomainsRequest]) (*connect.Response[userv1.ListAllowedDomainsResponse], error) {
	if _, err := userdto.ActorID(ctx); err != nil {
		return nil, api.ToConnectError(ctx, err)
	}
	domains, err := h.Logic.ListAllowedDomains(ctx)
	if err != nil {
		return nil, api.ToConnectError(ctx, err)
	}
	allowedDomains := make([]*userv1.AllowedDomain, len(domains))
	for i, domain := range domains {
		allowedDomains[i] = &userv1.AllowedDomain{Id: domain.Id.String(), Domain: domain.Domain}
	}
	response := connect.NewResponse(&userv1.ListAllowedDomainsResponse{AllowedDomains: allowedDomains})
	response.Header().Set("Cache-Control", "no-store")
	return response, nil
}

func (h *AllowedDomainHandler) AllowDomain(ctx context.Context, req *connect.Request[userv1.AllowDomainRequest]) (*connect.Response[userv1.AllowDomainResponse], error) {
	actorID, err := userdto.ActorID(ctx)
	if err != nil {
		return nil, api.ToConnectError(ctx, err)
	}
	if err := h.Logic.AllowDomain(ctx, req.Msg.Domain, actorID); err != nil {
		return nil, api.ToConnectError(ctx, err)
	}
	response := connect.NewResponse(&userv1.AllowDomainResponse{})
	response.Header().Set("Cache-Control", "no-store")
	return response, nil
}

func (h *AllowedDomainHandler) RemoveAllowedDomain(ctx context.Context, req *connect.Request[userv1.RemoveAllowedDomainRequest]) (*connect.Response[userv1.RemoveAllowedDomainResponse], error) {
	if _, err := userdto.ActorID(ctx); err != nil {
		return nil, api.ToConnectError(ctx, err)
	}
	id, err := uuid.Parse(req.Msg.Id)
	if err != nil || id == uuid.Nil {
		return nil, api.ToConnectError(ctx, errs.NewBadRequestError("invalid allowed domain ID"))
	}
	if err := h.Logic.RemoveAllowedDomain(ctx, id); err != nil {
		return nil, api.ToConnectError(ctx, err)
	}
	response := connect.NewResponse(&userv1.RemoveAllowedDomainResponse{})
	response.Header().Set("Cache-Control", "no-store")
	return response, nil
}
