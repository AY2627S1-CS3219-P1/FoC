package authorization

// ClaimsKey is the context key shared by authentication middleware and
// authorization interceptors. Authentication stores verified access claims here.
type ClaimsKey struct{}
