package models

import (
	"time"

	"github.com/google/uuid"
)

type RoleDispatch struct {
	ID, UserID, AdminID uuid.UUID
	FromRole, ToRole    RoleName
	Revision            int64
	Reason              string
	CreatedAt           time.Time
	AppliedAt           *time.Time
}
