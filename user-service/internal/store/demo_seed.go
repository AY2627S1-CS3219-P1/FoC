package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/models"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/seed"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// DemoSeed persists User Service fixtures using the same GORM models and
// transaction boundary as other User Service adapters.
type DemoSeed struct{ db *gorm.DB }

func NewDemoSeed(db *gorm.DB) *DemoSeed { return &DemoSeed{db: db} }

func (s *DemoSeed) SeedDemo(ctx context.Context, data seed.Dataset, reset bool) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, u := range data.Users {
			var matches []models.User
			if err := tx.Unscoped().Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? OR (email = ? AND deleted_at IS NULL)", u.ID, u.Email).Find(&matches).Error; err != nil {
				return err
			}
			for _, found := range matches {
				if found.ID != u.ID || !strings.EqualFold(found.Email, u.Email) {
					return fmt.Errorf("demo identity conflicts with an existing user: %s", u.Email)
				}
			}
			if len(matches) == 0 {
				if err := tx.Create(&u).Error; err != nil {
					return err
				}
			} else if reset {
				if err := tx.Unscoped().Model(&models.User{}).Where("id = ?", u.ID).
					Select("display_name", "description", "telegram_handle", "phone_number", "role", "deleted_at", "updated_at").Updates(&u).Error; err != nil {
					return err
				}
			}
		}
		// Keep an existing live allowlist entry, even if it has another ID.
		var domains []models.AllowedEmailDomain
		if err := tx.Unscoped().Where("id = ? OR (domain = ? AND deleted_at IS NULL)", data.Domain.ID, data.Domain.Domain).Find(&domains).Error; err != nil {
			return err
		}
		live := false
		for _, d := range domains {
			if d.ID == data.Domain.ID && d.Domain != data.Domain.Domain {
				return fmt.Errorf("demo domain identity conflicts with an existing domain")
			}
			if !d.DeletedAt.Valid {
				live = true
			}
		}
		if !live {
			if len(domains) == 0 {
				if err := tx.Create(&data.Domain).Error; err != nil {
					return err
				}
			} else if err := tx.Unscoped().Model(&models.AllowedEmailDomain{}).Where("id = ?", data.Domain.ID).Updates(map[string]any{"deleted_at": nil, "updated_at": data.Domain.UpdatedAt}).Error; err != nil {
				return err
			}
		}
		return tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "id"}}, DoNothing: true}).Create(&data.Suspension).Error
	}, &sql.TxOptions{Isolation: sql.LevelSerializable})
}
