// Permanent account-deletion persistence. The lifecycle flag is a GORM-managed
// column; the hard-delete removes the row for good and relies on the database's
// ON DELETE cascade for sessions, tokens, favourites and the deletion job.
package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/database/deletiondb"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/models"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// DeletionJob is one account's pending-deletion-job status. HasJob is false when
// the account is not permanently deleting, in which case every field is zero.
type DeletionJob struct {
	HasJob         bool
	LastCheckedAt  time.Time
	WaitingReasons []string
}

// DeletionJobs reads the account-deletion jobs table. It is a read-only adapter
// for the deletion-status query; the worker owns the rest of the table. An
// account only ever reads its own row, so the query takes no lock or lease.
type DeletionJobs struct{ queries *deletiondb.Queries }

func NewDeletionJobs(queries *deletiondb.Queries) *DeletionJobs {
	return &DeletionJobs{queries: queries}
}

// GetMyDeletionJob returns the caller's own pending-job status. A missing job is
// treated as no pending job rather than an error, so a status query for a live
// account is never a failure.
func (s *DeletionJobs) GetMyDeletionJob(ctx context.Context, userID uuid.UUID) (DeletionJob, error) {
	row, err := s.queries.GetDeletionJob(ctx, userID)
	if errors.Is(err, sql.ErrNoRows) {
		return DeletionJob{}, nil
	}
	if err != nil {
		return DeletionJob{}, err
	}
	return DeletionJob{
		HasJob:         true,
		LastCheckedAt:  row.LastCheckedAt.Time,
		WaitingReasons: row.WaitingReasons,
	}, nil
}

// AcceptDeletion sets the one-way flag and its immutable timestamp on the given
// row. The users_deletion_irreversible trigger only rejects clearing the flag or
// moving an established request time, so a false -> true / NULL -> value change
// is accepted and every other update is untouched.
func (s *Users) AcceptDeletion(ctx context.Context, id uuid.UUID, requestedAt time.Time) error {
	return s.db.WithContext(ctx).Model(&models.User{}).
		Where("id = ?", id).
		Updates(map[string]any{"is_deleting": true, "deletion_requested_at": requestedAt}).Error
}

// HardDelete removes the user and the cascade-linked sessions, tokens and
// favourites for good. Unlike Delete it does not set a soft-delete marker, so the
// row is gone and the retained-history foreign keys clear to NULL.
func (s *Users) HardDelete(ctx context.Context, id uuid.UUID) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, model := range []any{&models.Session{}, &models.AuthToken{}, &models.FavouriteSupplier{}} {
			if err := tx.Where("user_id = ?", id).Delete(model).Error; err != nil {
				return err
			}
		}
		res := tx.Unscoped().Delete(&models.User{}, id)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return ErrNotFound
		}
		return nil
	})
}
