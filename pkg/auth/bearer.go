package auth

import "context"

type userBearerKey struct{}

// WithUserBearer carries a verified user's Authorization header for delegation
// to another service. Authentication middleware must verify it before calling
// this helper. The credential must not be persisted or used as a service token.
func WithUserBearer(ctx context.Context, authorization string) context.Context {
	return context.WithValue(ctx, userBearerKey{}, authorization)
}

// UserBearerFromContext returns the user's Bearer authorization header, including
// the scheme, for forwarding to another service.
func UserBearerFromContext(ctx context.Context) (string, bool) {
	authorization, ok := ctx.Value(userBearerKey{}).(string)
	return authorization, ok && authorization != ""
}
