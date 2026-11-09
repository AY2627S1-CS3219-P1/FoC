package router_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth/httpauth"
	userv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/v1/userv1connect"
	pagination "github.com/AY2627S1-CS3219-P1/FoC/pkg/pagination"
	allowedadmin "github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/admin"
	adminhandler "github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/handlers/admin"
	authhandler "github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/handlers/auth"
	profilehandler "github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/handlers/profile"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/jwt"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/models"
	review "github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/review"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/router"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/service"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/store"
	"github.com/google/uuid"
)

type protectedUsers struct{ users map[uuid.UUID]models.User }

func (p *protectedUsers) GetByID(_ context.Context, id uuid.UUID) (*models.User, error) {
	u, ok := p.users[id]
	if !ok {
		return nil, store.ErrNotFound
	}
	return &u, nil
}

type profileStub struct{ user models.User }

func (p profileStub) GetMyProfile(context.Context, uuid.UUID) (models.User, error) {
	return p.user, nil
}

func (p profileStub) UpdateMyProfile(context.Context, uuid.UUID, service.ProfileInput) (models.User, error) {
	return p.user, nil
}

type adminStub struct{ user models.User }

func (a adminStub) GetUserByEmail(context.Context, uuid.UUID, string) (models.User, error) {
	return a.user, nil
}

func (a adminStub) ChangeUserRole(context.Context, uuid.UUID, uuid.UUID, models.RoleName, string) (models.User, error) {
	return a.user, nil
}

type reviewStub struct{}

func (reviewStub) SubmitReview(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, review.ReviewSubmission) error {
	return nil
}

func (reviewStub) ListReviewsForUser(context.Context, uuid.UUID, pagination.Pagination) (review.ReviewPage, error) {
	return review.ReviewPage{}, nil
}

func (reviewStub) ListReviewsFromUser(context.Context, uuid.UUID, pagination.Pagination) (review.ReviewPage, error) {
	return review.ReviewPage{}, nil
}

func (reviewStub) RemoveReview(context.Context, uuid.UUID, string, uuid.UUID) error { return nil }

func (reviewStub) ListReviewRemovals(context.Context, pagination.Pagination) (review.RemovalPage, error) {
	return review.RemovalPage{}, nil
}

type allowedDomainStub struct {
	calls  atomic.Int32
	domain atomic.Value
}

func (s *allowedDomainStub) ListAllowedDomains(context.Context) ([]allowedadmin.AllowedDomain, error) {
	s.calls.Add(1)
	return nil, nil
}

func (s *allowedDomainStub) AllowDomain(_ context.Context, domain string, _ uuid.UUID) error {
	s.domain.Store(domain)
	s.calls.Add(1)
	return nil
}

func (s *allowedDomainStub) RemoveAllowedDomain(context.Context, uuid.UUID) error {
	s.calls.Add(1)
	return nil
}

func TestProtectedConnectRoutes(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	codec, err := jwt.NewES256Codec(key, "test-key", httpauth.TokenIssuer, httpauth.TokenAudience)
	if err != nil {
		t.Fatal(err)
	}
	actorID, targetID := uuid.New(), uuid.New()
	actor := models.User{ID: actorID, Email: "actor@example.com", DisplayName: "Actor", Role: models.RoleUser}
	contact := "+6590000000"
	target := models.User{ID: targetID, Email: "target@example.com", DisplayName: "Target",
		PhoneNumber: &contact, Role: models.RoleUser}
	users := &protectedUsers{users: map[uuid.UUID]models.User{actorID: actor, targetID: target}}
	handler := router.Setup(testHealth(), &authhandler.Handler{Logic: &stubLogic{}, AllowedOrigin: frontendOrigin},
		router.ProtectedRoutes{
			Profile:      &profilehandler.Handler{Logic: profileStub{actor}},
			Admin:        &adminhandler.Handler{Logic: adminStub{target}},
			Review:       &review.Handler{Logic: reviewStub{}},
			Authenticate: httpauth.AuthenticateLocal(codec.VerifyAccess), Users: users,
		})
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	profileClient := userv1connect.NewProfileServiceClient(server.Client(), server.URL)
	adminClient := userv1connect.NewUserAdminServiceClient(server.Client(), server.URL)
	ctx := context.Background()
	if _, err := profileClient.GetMyProfile(
		ctx,
		connect.NewRequest(&userv1.GetMyProfileRequest{}),
	); connect.CodeOf(
		err,
	) != connect.CodeUnauthenticated {
		t.Fatalf("missing bearer token: %v", err)
	}
	token := signedTestAccess(t, codec, actorID, jwt.RoleUser)
	get := connect.NewRequest(&userv1.GetMyProfileRequest{})
	get.Header().Set("Authorization", "Bearer "+token)
	response, err := profileClient.GetMyProfile(ctx, get)
	if err != nil || response.Msg.Profile.Id != actorID.String() || response.Msg.Profile.Email != actor.Email {
		t.Fatalf("own profile: %+v, %v", response, err)
	}
	lookup := connect.NewRequest(&userv1.GetUserByEmailRequest{Email: target.Email})
	lookup.Header().Set("Authorization", "Bearer "+token)
	if _, err := adminClient.GetUserByEmail(ctx, lookup); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("ordinary user lookup: %v", err)
	}
	profileUpdate := connect.NewRequest(&userv1.UpdateMyProfileRequest{
		DisplayName: " " + strings.Repeat("N", 100) + " ",
		PhoneNumber: " +123 ",
	})
	profileUpdate.Header().Set("Authorization", "Bearer "+token)
	if _, err := profileClient.UpdateMyProfile(ctx, profileUpdate); err != nil {
		t.Fatalf("normalized profile input rejected by RPC validation: %v", err)
	}
	actor.Role = models.RoleSuspended
	users.users[actorID] = actor
	update := connect.NewRequest(&userv1.UpdateMyProfileRequest{DisplayName: "New", PhoneNumber: "+123"})
	update.Header().Set("Authorization", "Bearer "+token)
	if _, err := profileClient.UpdateMyProfile(ctx, update); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("suspended update with stale user token: %v", err)
	}
	actor.Role = models.RoleAdmin
	users.users[actorID] = actor
	lookup = connect.NewRequest(&userv1.GetUserByEmailRequest{Email: target.Email})
	lookup.Header().Set("Authorization", "Bearer "+token)
	if response, err := adminClient.GetUserByEmail(
		ctx,
		lookup,
	); err != nil ||
		response.Msg.User.Id != targetID.String() {
		t.Fatalf("persisted admin lookup: %+v, %v", response, err)
	}
	actor.Role = models.RoleUser
	users.users[actorID] = actor
	staleAdminToken := signedTestAccess(t, codec, actorID, jwt.RoleAdmin)
	change := connect.NewRequest(
		&userv1.ChangeUserRoleRequest{UserId: targetID.String(), ToRole: userv1.UserRole_USER_ROLE_ADMIN},
	)
	change.Header().Set("Authorization", "Bearer "+staleAdminToken)
	if _, err := adminClient.ChangeUserRole(ctx, change); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("stale admin token: %v", err)
	}
}

func TestAllowedDomainAuthorization(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	codec, err := jwt.NewES256Codec(key, "test-key", httpauth.TokenIssuer, httpauth.TokenAudience)
	if err != nil {
		t.Fatal(err)
	}
	actorID := uuid.New()
	users := &protectedUsers{users: make(map[uuid.UUID]models.User)}
	logic := &allowedDomainStub{}
	handler := router.Setup(testHealth(), &authhandler.Handler{Logic: &stubLogic{}, AllowedOrigin: frontendOrigin},
		router.ProtectedRoutes{
			Profile:        &profilehandler.Handler{Logic: profileStub{}},
			Admin:          &adminhandler.Handler{Logic: adminStub{}},
			AllowedDomains: &allowedadmin.AllowedDomainHandler{Logic: logic},
			Authenticate:   httpauth.AuthenticateLocal(codec.VerifyAccess), Users: users,
		})
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client := userv1connect.NewAllowedDomainAdminServiceClient(server.Client(), server.URL)
	ctx := context.Background()
	operations := []struct {
		name string
		call func(string) error
	}{
		{"list", func(token string) error {
			req := connect.NewRequest(&userv1.ListAllowedDomainsRequest{})
			req.Header().Set("Authorization", token)
			_, err := client.ListAllowedDomains(ctx, req)
			return err
		}},
		{"allow", func(token string) error {
			req := connect.NewRequest(&userv1.AllowDomainRequest{Domain: "example.com"})
			req.Header().Set("Authorization", token)
			_, err := client.AllowDomain(ctx, req)
			return err
		}},
		{"remove", func(token string) error {
			req := connect.NewRequest(&userv1.RemoveAllowedDomainRequest{Id: uuid.NewString()})
			req.Header().Set("Authorization", token)
			_, err := client.RemoveAllowedDomain(ctx, req)
			return err
		}},
	}
	for _, tc := range []struct {
		name          string
		role          models.RoleName
		tokenRole     jwt.Role
		wantListCode  connect.Code
		wantWriteCode connect.Code
	}{
		{"super admin", models.RoleSuperAdmin, jwt.RoleSuperAdmin, 0, 0},
		{"admin", models.RoleAdmin, jwt.RoleAdmin, 0, connect.CodePermissionDenied},
		{"user", models.RoleUser, jwt.RoleUser, connect.CodePermissionDenied, connect.CodePermissionDenied},
		{"suspended", models.RoleSuspended, jwt.RoleSuspendedUser, connect.CodePermissionDenied, connect.CodePermissionDenied},
		{"missing credentials", models.RoleSuperAdmin, "", connect.CodeUnauthenticated, connect.CodeUnauthenticated},
		{"demoted super admin", models.RoleAdmin, jwt.RoleSuperAdmin, 0, connect.CodePermissionDenied},
	} {
		t.Run(tc.name, func(t *testing.T) {
			users.users[actorID] = models.User{ID: actorID, Role: tc.role}
			var token string
			if tc.tokenRole != "" {
				token = "Bearer " + signedTestAccess(t, codec, actorID, tc.tokenRole)
			}
			for _, operation := range operations {
				t.Run(operation.name, func(t *testing.T) {
					wantCode := tc.wantWriteCode
					if operation.name == "list" {
						wantCode = tc.wantListCode
					}
					before := logic.calls.Load()
					err := operation.call(token)
					var wantCalls int32
					if wantCode == 0 {
						wantCalls = 1
						if err != nil {
							t.Fatalf("authorized request: %v", err)
						}
					} else if connect.CodeOf(err) != wantCode {
						t.Fatalf("want %v, got %v", wantCode, err)
					}
					if calls := logic.calls.Load() - before; calls != wantCalls {
						t.Fatalf("domain logic calls = %d, want %d", calls, wantCalls)
					}
				})
			}
		})
	}

	users.users[actorID] = models.User{ID: actorID, Role: models.RoleSuperAdmin}
	token := "Bearer " + signedTestAccess(t, codec, actorID, jwt.RoleSuperAdmin)
	for _, tc := range []struct {
		domain   string
		wantCode connect.Code
	}{
		{" \tEXAMPLE.COM\n", 0},
		{"\u2003Example.COM\u2003", 0},
		{"   ", connect.CodeInvalidArgument},
		{" example .com ", connect.CodeInvalidArgument},
		{" https://example.com ", connect.CodeInvalidArgument},
	} {
		t.Run("hostname/"+tc.domain, func(t *testing.T) {
			req := connect.NewRequest(&userv1.AllowDomainRequest{Domain: tc.domain})
			req.Header().Set("Authorization", token)
			before := logic.calls.Load()
			_, err := client.AllowDomain(ctx, req)
			if tc.wantCode == 0 {
				if err != nil || logic.domain.Load() != "example.com" || logic.calls.Load() != before+1 {
					t.Fatalf("normalized domain = %v, err = %v", logic.domain.Load(), err)
				}
			} else if connect.CodeOf(err) != tc.wantCode || logic.calls.Load() != before {
				t.Fatalf("invalid hostname reached domain logic or returned wrong code: %v", err)
			}
		})
	}
}

func signedTestAccess(t *testing.T, codec *jwt.ES256Codec, subject uuid.UUID, role jwt.Role) string {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	token, err := codec.Sign(jwt.Claims{Type: jwt.AccessToken, Subject: subject.String(),
		SessionID: "00000000-0000-4000-8000-000000000001", Role: role, IssuedAt: now,
		ExpiresAt: now.Add(time.Minute), TokenID: "00000000-0000-4000-8000-000000000002"})
	if err != nil {
		t.Fatal(err)
	}
	return token
}
