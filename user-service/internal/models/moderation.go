// Role change history.

package models

import (
	"github.com/google/uuid"
)

type RoleChange struct {
	BaseModel
	Userstamps
	UserID   uuid.UUID
	FromRole RoleName
	ToRole   RoleName
	Reason   *string
	ReportID *uuid.UUID
}
