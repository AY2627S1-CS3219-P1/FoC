package store

import (
	"context"
	"errors"
	"testing"

	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/models"
)

func TestProfileAndAuditedRolePersistence(t *testing.T) {
	ctx := context.Background()
	s, db := setupAuthStore(t)
	actor := createStoreUser(t, s, "actor@example.com")
	target := createStoreUser(t, s, "target@example.com")
	if err := db.Model(&actor).Update("role", models.RoleSuperAdmin).Error; err != nil {
		t.Fatal(err)
	}
	reason := "investigation"
	change := &models.RoleChange{UserID: target.ID, FromRole: models.RoleUser,
		ToRole: models.RoleSuspended, Reason: &reason,
		Userstamps: models.Userstamps{CreatedBy: &actor.ID, UpdatedBy: &actor.ID}}
	if err := s.WithTransaction(ctx, func(tx *Store) error {
		if _, err := tx.Users.GetByIDForUpdate(ctx, actor.ID); err != nil {
			return err
		}
		return tx.Admin.ChangeRole(ctx, change)
	}); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Users.GetByID(ctx, target.ID); err != nil || got.Role != models.RoleSuspended {
		t.Fatalf("suspended role = %+v, %v", got, err)
	}
	if changes, err := s.Admin.RoleChanges(ctx, target.ID); err != nil || len(changes) != 1 || changes[0].CreatedBy == nil || *changes[0].CreatedBy != actor.ID {
		t.Fatalf("role audit = %+v, %v", changes, err)
	}
	stale := &models.RoleChange{UserID: target.ID, FromRole: models.RoleUser,
		ToRole:     models.RoleAdmin,
		Userstamps: models.Userstamps{CreatedBy: &actor.ID, UpdatedBy: &actor.ID}}
	if err := s.Admin.ChangeRole(ctx, stale); !errors.Is(err, ErrRoleConflict) {
		t.Fatalf("stale role change: %v", err)
	}
	if changes, err := s.Admin.RoleChanges(ctx, target.ID); err != nil || len(changes) != 1 {
		t.Fatalf("stale change affected audit: %+v, %v", changes, err)
	}
	profile, err := s.Users.GetByID(ctx, target.ID)
	if err != nil {
		t.Fatal(err)
	}
	profile.DisplayName = "Changed"
	if err := s.Users.UpdateActiveProfile(ctx, profile); !errors.Is(err, ErrRoleConflict) {
		t.Fatalf("suspended profile update: %v", err)
	}
}
