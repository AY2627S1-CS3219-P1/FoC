package httpauth

import (
	"context"
	"errors"
	"net/http"
	"time"

	"connectrpc.com/connect"
	userv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/v1"
)

// currentRole resolves authorization separately from JWT identity. Never cache
// roles: a token issued before suspension must not retain its former abilities.
// Suspension still authenticates; domain operations decide which actions remain
// available to existing participants.
func (a *Authenticator) currentRole(ctx context.Context, token, subject string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	req := connect.NewRequest(&userv1.GetMyProfileRequest{})
	req.Header().Set("Authorization", "Bearer "+token)
	response, err := a.profileClient.GetMyProfile(ctx, req)
	if err != nil {
		if connect.CodeOf(err) == connect.CodeUnauthenticated || connect.CodeOf(err) == connect.CodeNotFound {
			return "", errInvalidAccessToken
		}
		return "", errRoleUnavailable
	}
	profile := response.Msg.GetProfile()
	if profile == nil || profile.Id != subject {
		return "", errRoleUnavailable
	}
	switch profile.Role {
	case userv1.UserRole_USER_ROLE_SUPER_ADMIN:
		return "super_admin", nil
	case userv1.UserRole_USER_ROLE_ADMIN:
		return "admin", nil
	case userv1.UserRole_USER_ROLE_USER:
		return "user", nil
	case userv1.UserRole_USER_ROLE_SUSPENDED_USER:
		return "suspended_user", nil
	default:
		return "", errRoleUnavailable
	}
}

var errRoleUnavailable = errors.New("current account role unavailable")

type roleUnavailableError struct{}

func (roleUnavailableError) Error() string                { return errRoleUnavailable.Error() }
func (e roleUnavailableError) ErrorTrace() string         { return e.Error() }
func (roleUnavailableError) Code() int                    { return http.StatusServiceUnavailable }
func (roleUnavailableError) GetConnectCode() connect.Code { return connect.CodeUnavailable }
