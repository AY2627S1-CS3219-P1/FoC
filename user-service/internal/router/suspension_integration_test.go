package router_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"

	"connectrpc.com/connect"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth/httpauth"
	userv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/v1/userv1connect"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/database"
	adminhandler "github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/handlers/admin"
	authhandler "github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/handlers/auth"
	profilehandler "github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/handlers/profile"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/jwt"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/models"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/router"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/service"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/store"
	"github.com/google/uuid"
)

type suspensionAuth struct {
	stubLogic
	codec *jwt.ES256Codec
}

// Test-only acknowledgment seam. Production has no such fallback; #29 must
// supply authenticated User-to-Order transport before suspension is admitted.
type suspensionEffects struct {
	mu   sync.Mutex
	fail bool
	ids  []uuid.UUID
}

func (e *suspensionEffects) ApplyRoleDecision(_ context.Context, d models.RoleDispatch) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.ids = append(e.ids, d.ID)
	if e.fail {
		return context.DeadlineExceeded
	}
	return nil
}

func (s *suspensionAuth) PublicKeys() (jwt.JWKSet, error) { return s.codec.PublicKeys(), nil }

// This test uses an explicitly disposable database, like the store tests.
func TestAdminSuspensionReachesExistingSession(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	t.Setenv("APP_ENV", "local")
	ctx := context.Background()
	db, err := database.Open(dsn, 5, 5)
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("TRUNCATE users CASCADE").Error; err != nil {
		t.Fatal(err)
	}
	persistence := store.New(db)
	admin := models.User{Email: "admin@example.com", DisplayName: "Admin", Role: models.RoleAdmin}
	user := models.User{Email: "user@example.com", DisplayName: "User", Role: models.RoleUser}
	for _, u := range []*models.User{&admin, &user} {
		if err := persistence.Users.Create(ctx, u); err != nil {
			t.Fatal(err)
		}
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	codec, err := jwt.NewES256Codec(key, "suspension-test", httpauth.TokenIssuer, httpauth.TokenAudience)
	if err != nil {
		t.Fatal(err)
	}
	effects := &suspensionEffects{}
	roles := &service.RoleService{Users: persistence.Users, Dispatches: persistence.Admin, Effects: effects, WithTransaction: func(ctx context.Context, fn func(service.RoleStore) error) error {
		return persistence.WithTransaction(ctx, func(tx *store.Store) error {
			return fn(service.RoleStore{Users: tx.Users, Admin: tx.Admin, Dispatches: tx.Admin})
		})
	}}
	server := httptest.NewServer(router.Setup(testHealth(), &authhandler.Handler{Logic: &suspensionAuth{codec: codec}, AllowedOrigin: frontendOrigin}, router.ProtectedRoutes{
		Profile:      &profilehandler.Handler{Logic: &service.ProfileService{Users: persistence.Users}},
		Admin:        &adminhandler.Handler{Logic: roles},
		Authenticate: httpauth.AuthenticateLocal(codec.VerifyAccess), Users: persistence.Users,
	}))
	t.Cleanup(server.Close)
	adminClient := userv1connect.NewUserAdminServiceClient(server.Client(), server.URL)
	adminToken := signedTestAccess(t, codec, admin.ID, jwt.RoleAdmin)
	userToken := signedTestAccess(t, codec, user.ID, jwt.RoleUser)
	authenticator, err := httpauth.NewAuthenticator(ctx, httpauth.AuthConfig{UserServiceURL: server.URL, Issuer: httpauth.TokenIssuer, Audience: httpauth.TokenAudience})
	if err != nil {
		t.Fatal(err)
	}
	var caller auth.Caller
	protected := authenticator.Authenticate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		caller, _ = auth.CallerFromContext(r.Context())
		w.WriteHeader(http.StatusNoContent)
	}))
	assertRole := func(want string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/protected", nil)
		req.Header.Set("Authorization", "Bearer "+userToken)
		response := httptest.NewRecorder()
		protected.ServeHTTP(response, req)
		if response.Code != http.StatusNoContent || caller.ID != user.ID.String() || caller.Role != want || caller.Admin {
			t.Fatalf("same session: HTTP %d, caller %+v; want role %s", response.Code, caller, want)
		}
	}
	change := func(to userv1.UserRole, reason string) error {
		req := connect.NewRequest(&userv1.ChangeUserRoleRequest{UserId: user.ID.String(), ToRole: to, Reason: reason})
		req.Header().Set("Authorization", "Bearer "+adminToken)
		_, err := adminClient.ChangeUserRole(ctx, req)
		return err
	}
	assertRole("user")
	roles.Effects = nil
	if err := change(userv1.UserRole_USER_ROLE_SUSPENDED_USER, "transport unavailable"); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("production default did not fail closed: %v", err)
	}
	if pending, err := persistence.Admin.PendingRoleDispatch(ctx, user.ID); err != nil || pending != nil {
		t.Fatalf("unconfigured transport admitted intent: %+v %v", pending, err)
	}
	roles.Effects = effects
	for range 3 {
		if _, err := persistence.Admin.CreateWarning(ctx, &models.AccountWarning{UserID: user.ID, RequestID: uuid.New(), Reason: "warning only"}); err != nil {
			t.Fatal(err)
		}
	}
	assertRole("user")
	if err := change(userv1.UserRole_USER_ROLE_SUSPENDED_USER, "  "); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("blank suspension reason: %v", err)
	}
	assertRole("user")
	if err := change(userv1.UserRole_USER_ROLE_SUSPENDED_USER, "  explicit admin decision  "); err != nil {
		t.Fatal(err)
	}
	assertRole("suspended_user")
	changes, err := persistence.Admin.RoleChanges(ctx, user.ID)
	if err != nil || len(changes) != 1 || changes[0].Reason == nil || *changes[0].Reason != "explicit admin decision" || changes[0].CreatedBy == nil || *changes[0].CreatedBy != admin.ID {
		t.Fatalf("suspension audit: %+v, %v", changes, err)
	}
	if err := change(userv1.UserRole_USER_ROLE_USER, "appeal granted"); err != nil {
		t.Fatal(err)
	}
	assertRole("user")
	effects.mu.Lock()
	effects.fail = true
	effects.mu.Unlock()
	if err := change(userv1.UserRole_USER_ROLE_SUSPENDED_USER, "lost Order acknowledgment"); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("uncertain dispatch: %v", err)
	}
	assertRole("user")
	pending, err := persistence.Admin.PendingRoleDispatch(ctx, user.ID)
	if err != nil || pending == nil || pending.Reason != "lost Order acknowledgment" {
		t.Fatalf("durable handoff: %+v %v", pending, err)
	}
	before, err := persistence.Admin.RoleChanges(ctx, user.ID)
	if err != nil || len(before) != 2 {
		t.Fatalf("premature role finalization: %+v %v", before, err)
	}
	effects.mu.Lock()
	effects.fail = false
	effects.mu.Unlock()
	restarted := *roles
	if err := restarted.RecoverRoleDispatchesOnce(ctx); err != nil {
		t.Fatal(err)
	}
	assertRole("suspended_user")
	if err := restarted.RecoverRoleDispatchesOnce(ctx); err != nil {
		t.Fatal(err)
	}
	effects.mu.Lock()
	defer effects.mu.Unlock()
	if len(effects.ids) != 4 || effects.ids[2] != pending.ID || effects.ids[3] != pending.ID {
		t.Fatalf("recovery changed/repeated operation identity: %v", effects.ids)
	}
}
