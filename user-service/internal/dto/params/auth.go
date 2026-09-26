package params

type MagicLinkRequest struct {
	Email string `json:"email" validate:"required,email"`
}

type LoginToken struct {
	Token string `json:"token" validate:"required"`
}

type UserRegistration struct {
	Token       string `json:"token" validate:"required"`
	DisplayName string `json:"displayName" validate:"required,max=100"`
}
