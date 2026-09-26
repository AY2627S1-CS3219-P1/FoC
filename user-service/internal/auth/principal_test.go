package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"user-service/internal/models"
)

const (
	sa  = models.RoleSuperAdmin
	ad  = models.RoleAdmin
	us  = models.RoleUser
	sus = models.RoleSuspended
)

func TestRolePredicates(t *testing.T) {
	cases := []struct {
		role                               models.RoleName
		isAdmin, isSuperAdmin, isSuspended bool
	}{
		{sa, true, true, false},
		{ad, true, false, false},
		{us, false, false, false},
		{sus, false, false, true},
	}
	for _, c := range cases {
		p := Principal{Role: c.role}
		if p.IsAdmin() != c.isAdmin || p.IsSuperAdmin() != c.isSuperAdmin || p.IsSuspended() != c.isSuspended {
			t.Errorf("%s: admin=%v super=%v suspended=%v", c.role, p.IsAdmin(), p.IsSuperAdmin(), p.IsSuspended())
		}
	}
}

func TestCanManage(t *testing.T) {
	want := map[models.RoleName][]models.RoleName{
		sa:  {ad, us, sus},
		ad:  {us, sus},
		us:  {},
		sus: {},
	}
	for actor, allowed := range want {
		p := Principal{Role: actor}
		ok := map[models.RoleName]bool{}
		for _, r := range allowed {
			ok[r] = true
		}
		for _, target := range models.AllRoles {
			if got := p.CanManage(target); got != ok[target] {
				t.Errorf("%s manages %s = %v, want %v", actor, target, got, ok[target])
			}
		}
	}
}

func TestCanAssignRole(t *testing.T) {
	other := uuid.New()
	cases := []struct {
		name          string
		actor         models.RoleName
		from, to      models.RoleName
		self, allowed bool
	}{
		// admin: user <-> suspended only
		{"admin suspends user", ad, us, sus, false, true},
		{"admin reinstates user", ad, sus, us, false, true},
		{"admin cannot promote to admin", ad, us, ad, false, false},
		{"admin cannot demote admin", ad, ad, us, false, false},
		{"admin cannot suspend admin", ad, ad, sus, false, false},
		{"admin cannot touch super_admin", ad, sa, us, false, false},

		// super_admin: everything admin can + admin promotions/demotions
		{"super suspends user", sa, us, sus, false, true},
		{"super reinstates user", sa, sus, us, false, true},
		{"super promotes user to admin", sa, us, ad, false, true},
		{"super demotes admin", sa, ad, us, false, true},
		{"super suspends admin", sa, ad, sus, false, true},
		{"super cannot create super", sa, ad, sa, false, false},
		{"super cannot demote super", sa, sa, ad, false, false},

		// nobody else, never self, never no-ops or unknown roles
		{"user cannot suspend", us, us, sus, false, false},
		{"suspended cannot reinstate", sus, sus, us, false, false},
		{"admin cannot self-suspend", ad, ad, sus, true, false},
		{"super cannot self-demote", sa, sa, ad, true, false},
		{"no-op change", sa, us, us, false, false},
		{"unknown role", sa, us, "root", false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			actorID := uuid.New()
			target := other
			if c.self {
				target = actorID
			}
			p := Principal{UserID: actorID, Role: c.actor}
			if got := p.CanAssignRole(target, c.from, c.to); got != c.allowed {
				t.Errorf("got %v, want %v", got, c.allowed)
			}
		})
	}
}

func TestContextAndRequireAuth(t *testing.T) {
	h := RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, _ := FromContext(r.Context())
		if p.Role != us {
			t.Errorf("role = %q", p.Role)
		}
		w.WriteHeader(http.StatusNoContent)
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no principal: %d", rec.Code)
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = req.WithContext(WithPrincipal(context.Background(), Principal{UserID: uuid.New(), Role: us}))
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("with principal: %d", rec.Code)
	}
}
