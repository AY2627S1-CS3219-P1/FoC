package seed

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/models"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/store"
	"gorm.io/gorm"
)

func ensureDomain(ctx context.Context, db *gorm.DB, fixture models.AllowedEmailDomain) error {
	var active models.AllowedEmailDomain
	err := db.Where("domain = ?", fixture.Domain).Take(&active).Error
	if err == nil {
		return nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	var existing models.AllowedEmailDomain
	err = db.Unscoped().Take(&existing, "id = ?", fixture.ID).Error
	if err == nil {
		if !strings.EqualFold(existing.Domain, fixture.Domain) {
			return fmt.Errorf("fixture registration domain identity conflicts with an existing domain")
		}
		return db.Unscoped().Model(&existing).Update("deleted_at", nil).Error
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	return store.NewAdmin(db).AddDomain(ctx, &fixture)
}
