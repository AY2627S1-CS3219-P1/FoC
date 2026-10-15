// Package seed owns opt-in local User Service fixtures, separate from migrations.
package seed

import (
	"context"
	"fmt"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/models"
	"github.com/google/uuid"
)

type Dataset struct {
	Users      []models.User
	Domain     models.AllowedEmailDomain
	Suspension models.RoleChange
}

type Store interface {
	SeedDemo(context.Context, Dataset, bool) error
}
type Service struct {
	Store Store
	Clock func() time.Time
}

func (s *Service) Run(ctx context.Context, reset bool) error {
	return s.Store.SeedDemo(ctx, DemoDataset(s.Clock().UTC()), reset)
}

// DemoDataset gives account identities a stable home in their owning service.
// No tokens, sessions or bootstrap state are included.
func DemoDataset(now time.Time) Dataset {
	id := func(n int) uuid.UUID { return uuid.MustParse(fmt.Sprintf("f0c00001-0000-4000-8000-%012d", n)) }
	base := func(n int) models.BaseModel { return models.BaseModel{ID: id(n), CreatedAt: now, UpdatedAt: now} }
	user := func(n int, email, name string, role models.RoleName, description string) models.User {
		return models.User{BaseModel: base(n), Email: email + "@demo.foc.test", DisplayName: "[Demo] " + name, Role: role, Description: description}
	}
	handle, phone := "demo_student", "+6500000000"
	users := []models.User{
		user(1, "superadmin", "Super Admin", models.RoleSuperAdmin, "Can promote/demote admins, suspend and reinstate other accounts."),
		user(2, "admin", "Admin", models.RoleAdmin, "Can manage locations, review proposals and suspend/reinstate ordinary users."),
		user(3, "student", "Student", models.RoleUser, "Owns demo requests. Try changing these profile fields."),
		user(4, "suspended", "Suspended Student", models.RoleSuspended, "Read-only profile. Use an administrator to reinstate this account."),
		user(5, "other", "Other Student", models.RoleUser, ""),
		user(6, "staff", "Other Admin", models.RoleAdmin, "Target for super-admin demotion and admin-to-admin permission tests."),
	}
	users[2].TelegramHandle = &handle
	users[2].PhoneNumber = &phone
	actor, reason := id(2), "[Demo] Seeded suspension for reinstatement testing"
	return Dataset{Users: users, Domain: models.AllowedEmailDomain{BaseModel: base(10), Domain: "demo.foc.test"}, Suspension: models.RoleChange{BaseModel: base(11), Userstamps: models.Userstamps{CreatedBy: &actor, UpdatedBy: &actor}, UserID: id(4), FromRole: models.RoleUser, ToRole: models.RoleSuspended, Reason: &reason}}
}
