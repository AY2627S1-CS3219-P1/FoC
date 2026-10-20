package store

import (
	"context"
	"testing"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/database"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/models"
	"github.com/google/uuid"
)

// Requirement: rollback cannot discard revision tombstones or restart numbering.
// A definite rejection must retain identity without automatic retry/finalization.
func TestRoleDispatchReviewDatabase(t *testing.T) {
	s, db := setupAuthStore(t)
	ctx := context.Background()
	actor := createStoreUser(t, s, "actor@example.com")
	target := createStoreUser(t, s, "target@example.com")
	provider, err := database.Migrator(db)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := db.Exec("TRUNCATE users CASCADE").Error; err != nil {
			t.Error(err)
		}
	}()

	for revision := int64(1); revision <= 3; revision++ {
		d := models.RoleDispatch{
			ID:        uuid.New(),
			UserID:    target.ID,
			AdminID:   actor.ID,
			FromRole:  models.RoleUser,
			ToRole:    models.RoleSuspended,
			Reason:    "explicit decision",
			CreatedAt: time.Now().UTC(),
		}
		if err := s.WithTransaction(ctx, func(tx *Store) error {
			if _, err := tx.Users.GetByIDForUpdate(ctx, target.ID); err != nil {
				return err
			}
			return tx.Admin.PrepareRoleDispatch(ctx, &d)
		}); err != nil {
			t.Fatal(err)
		}
		if d.Revision != revision {
			t.Fatalf("revision reset: %d", d.Revision)
		}
		if revision < 3 {
			if err := s.Admin.CompleteRoleDispatch(ctx, d.ID, time.Now().UTC()); err != nil {
				t.Fatal(err)
			}
		} else {
			if err := s.Admin.RejectRoleDispatch(ctx, d.ID, time.Now().UTC()); err != nil {
				t.Fatal(err)
			}
			unfinished, err := s.Admin.HasUnfinishedRoleDispatches(ctx)
			if err != nil || !unfinished {
				t.Fatalf("startup lost unfinished rejection: %t %v", unfinished, err)
			}
			rows, err := s.Admin.PendingRoleDispatches(ctx, 50)
			if err != nil || len(rows) != 0 {
				t.Fatalf("rejected decision retried: %+v %v", rows, err)
			}
			pending, err := s.Admin.PendingRoleDispatch(ctx, target.ID)
			if err != nil || pending == nil || pending.ID != d.ID || pending.RejectedAt == nil {
				t.Fatalf("lost rejected identity: %+v %v", pending, err)
			}
			if err := s.Admin.CompleteRoleDispatch(ctx, d.ID, time.Now().UTC()); err == nil {
				t.Fatal("rejected decision finalized")
			}
		}

		if _, err := provider.DownTo(ctx, 5); err == nil {
			t.Fatal("rollback discarded role revisions")
		}
		stored, err := s.Admin.GetRoleDispatch(ctx, d.ID)
		if err != nil || stored.Revision != revision {
			t.Fatalf("rollback lost tombstone: %+v %v", stored, err)
		}
		if _, err := provider.Up(ctx); err != nil {
			t.Fatal(err)
		}
	}
}

// Deleted targets have no role to finalize. Retain uncertain decisions as
// tombstones, but never let them starve live recovery or block startup.
func TestDeletedTargetDispatchRetainsHistoryWithoutBlockingStartup(t *testing.T) {
	s, db := setupAuthStore(t)
	ctx := context.Background()
	actor := createStoreUser(t, s, "deletion-actor@example.com")
	target := createStoreUser(t, s, "deleted-target@example.com")
	live := createStoreUser(t, s, "live-target@example.com")
	prepare := func(userID uuid.UUID) models.RoleDispatch {
		t.Helper()
		d := models.RoleDispatch{
			ID:        uuid.New(),
			UserID:    userID,
			AdminID:   actor.ID,
			FromRole:  models.RoleUser,
			ToRole:    models.RoleSuspended,
			Reason:    "explicit suspension",
			CreatedAt: time.Now().UTC(),
		}
		if err := s.WithTransaction(ctx, func(tx *Store) error {
			if _, err := tx.Users.GetByIDForUpdate(ctx, userID); err != nil {
				return err
			}
			return tx.Admin.PrepareRoleDispatch(ctx, &d)
		}); err != nil {
			t.Fatal(err)
		}
		return d
	}
	abandoned := prepare(target.ID)
	if err := s.Users.Delete(ctx, target.ID); err != nil {
		t.Fatal(err)
	}
	unfinished, err := s.Admin.HasUnfinishedRoleDispatches(ctx)
	if err != nil || unfinished {
		t.Fatalf("deleted target blocks startup: %t %v", unfinished, err)
	}
	rows, err := s.Admin.PendingRoleDispatches(ctx, 1)
	if err != nil || len(rows) != 0 {
		t.Fatalf("deleted target dispatched: %+v %v", rows, err)
	}
	stored, err := s.Admin.GetRoleDispatch(ctx, abandoned.ID)
	if err != nil || stored.ID != abandoned.ID || stored.UserID != abandoned.UserID || stored.AdminID != abandoned.AdminID || stored.Revision != abandoned.Revision || stored.Reason != abandoned.Reason || stored.AppliedAt != nil || stored.RejectedAt != nil {
		t.Fatalf("deleted intent lost or forged acknowledgment: %+v %v", stored, err)
	}
	current := prepare(live.ID)
	unfinished, err = s.Admin.HasUnfinishedRoleDispatches(ctx)
	if err != nil || !unfinished {
		t.Fatalf("live intent bypassed fail-closed startup: %t %v", unfinished, err)
	}
	rows, err = s.Admin.PendingRoleDispatches(ctx, 1)
	if err != nil || len(rows) != 1 || rows[0].ID != current.ID {
		t.Fatalf("deleted target starved live recovery: %+v %v", rows, err)
	}
	provider, err := database.Migrator(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.DownTo(ctx, 5); err == nil {
		t.Fatal("rollback erased deleted-target tombstone")
	}
}
