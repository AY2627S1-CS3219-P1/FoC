package userview

import (
	"time"

	"github.com/yihao03/reminding/internal/database/sqlc"
)

type UserView struct {
	FirebaseUID string    `json:"uid"`
	Email       string    `json:"email"`
	Name        string    `json:"displayName"`
	DateOfBirth time.Time `json:"dateOfBirth"`
}

func ToUserView(user *sqlc.User) *UserView {
	return &UserView{
		FirebaseUID: user.FirebaseUid,
		Email:       user.Email,
		Name:        user.DisplayName,
		DateOfBirth: user.DateOfBirth.Time,
	}
}
