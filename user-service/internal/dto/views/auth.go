package views

type User struct {
	ID          string `json:"id"`
	Email       string `json:"email"`
	DisplayName string `json:"displayName"`
	Role        string `json:"role"`
}

// Session returns an access token for the client to hold in memory.
type Session struct {
	User        User   `json:"user"`
	AccessToken string `json:"accessToken"`
}

type RefreshedSession struct {
	AccessToken string `json:"accessToken"`
}
