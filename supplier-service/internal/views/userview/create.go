package userview

import (
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/database"
	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/database/userdb"
)

type CreateUserView struct {
	FirebaseUID string    `json:"uid" validate:"required"`
	Email       string    `json:"email" validate:"required,email"`
	DisplayName string    `json:"displayName" validate:"required,max=100"`
	DateOfBirth time.Time `json:"dateOfBirth" validate:"required,notfuture"`
}

func (v *CreateUserView) ToCreateUserParams() *userdb.CreateUserParams {
	return &userdb.CreateUserParams{
		FirebaseUid: v.FirebaseUID,
		Email:       v.Email,
		DisplayName: v.DisplayName,
		DateOfBirth: database.ToPGDate(&v.DateOfBirth),
	}
}
