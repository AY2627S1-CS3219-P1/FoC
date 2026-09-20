package userview

type AuthView struct {
	UserToken string `json:"idToken" validate:"required"`
}
