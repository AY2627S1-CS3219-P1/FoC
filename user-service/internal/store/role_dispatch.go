package store

import (
	"context"
	"errors"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/models"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

func (s *Admin) PendingRoleDispatch(ctx context.Context, userID uuid.UUID) (*models.RoleDispatch, error) {
	var d models.RoleDispatch
	err := s.db.WithContext(ctx).Where("user_id = ? AND applied_at IS NULL", userID).Take(&d).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &d, err
}

func (s *Admin) GetRoleDispatch(ctx context.Context, id uuid.UUID) (models.RoleDispatch, error) {
	var d models.RoleDispatch
	err := s.db.WithContext(ctx).Take(&d, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return d, ErrNotFound
	}
	return d, err
}

// Caller holds the target User row lock before these relationship reads/writes.
func (s *Admin) PrepareRoleDispatch(ctx context.Context, d *models.RoleDispatch) error {
	var revision int64
	if err := s.db.WithContext(ctx).Model(&models.RoleDispatch{}).Select("COALESCE(MAX(revision),0)+1").Where("user_id = ?", d.UserID).Scan(&revision).Error; err != nil {
		return err
	}
	d.Revision = revision
	return s.db.WithContext(ctx).Create(d).Error
}

func (s *Admin) CompleteRoleDispatch(ctx context.Context, id uuid.UUID, now time.Time) error {
	res := s.db.WithContext(ctx).Model(&models.RoleDispatch{}).Where("id = ? AND applied_at IS NULL AND rejected_at IS NULL", id).Update("applied_at", now)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected != 1 {
		return ErrRoleConflict
	}
	return nil
}

func (s *Admin) PendingRoleDispatches(ctx context.Context, limit int) ([]models.RoleDispatch, error) {
	var rows []models.RoleDispatch
	err := s.db.WithContext(ctx).
		Where("applied_at IS NULL AND rejected_at IS NULL").
		Where("EXISTS (SELECT 1 FROM users WHERE users.id = role_dispatches.user_id AND users.deleted_at IS NULL)").
		Order("created_at,id").Limit(limit).Find(&rows).Error
	return rows, err
}

// A rejected immutable decision stays pending for explicit reconciliation. It
// cannot finalize a role, disappear, or be replaced by an automatic retry.
func (s *Admin) RejectRoleDispatch(ctx context.Context, id uuid.UUID, now time.Time) error {
	res := s.db.WithContext(ctx).Model(&models.RoleDispatch{}).Where("id = ? AND applied_at IS NULL", id).Update("rejected_at", now)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected != 1 {
		return ErrRoleConflict
	}
	return nil
}

// Includes rejected decisions for live targets. Deleted-target decisions remain
// as immutable history, but cannot finalize a role or block unrelated startup.
func (s *Admin) HasUnfinishedRoleDispatches(ctx context.Context) (bool, error) {
	var exists bool
	err := s.db.WithContext(ctx).Raw(`SELECT EXISTS(SELECT 1 FROM role_dispatches d
 WHERE d.applied_at IS NULL AND EXISTS (
 SELECT 1 FROM users u WHERE u.id=d.user_id AND u.deleted_at IS NULL))`).Scan(&exists).Error
	return exists, err
}
