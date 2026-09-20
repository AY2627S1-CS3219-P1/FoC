package userview

import (
	"time"

	"github.com/yihao03/reminding/internal/database"
	"github.com/yihao03/reminding/internal/database/sqlc"
)

type CreateUserView struct {
	FirebaseUID string    `json:"uid" validate:"required"`
	Email       string    `json:"email" validate:"required,email"`
	DisplayName string    `json:"displayName" validate:"required,max=100"`
	DateOfBirth time.Time `json:"dateOfBirth" validate:"required,notfuture"`
}

func (v *CreateUserView) ToCreateUserParams() *sqlc.CreateUserParams {
	return &sqlc.CreateUserParams{
		FirebaseUid: v.FirebaseUID,
		Email:       v.Email,
		DisplayName: v.DisplayName,
		DateOfBirth: database.ToPGDate(&v.DateOfBirth),
	}
}
