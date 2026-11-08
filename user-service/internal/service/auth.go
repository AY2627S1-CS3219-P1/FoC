package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"net/mail"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api/errs"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/email"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/accountdeletion"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/jwt"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/models"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/store"
	"github.com/google/uuid"
)

var (
	ErrEmailDomainNotAllowed = errs.NewForbiddenError("email domain is not allowed")
	// ErrProtectedAccount rejects deletion of administrator and bootstrap
	// accounts before acceptance changes any state.
	ErrProtectedAccount = errs.NewFailedPreconditionError("administrator and bootstrap accounts cannot be deleted")
)

const (
	MagicLinkLifetime = 10 * time.Minute
	// TODO: A revoked session may still have a valid access JWT for up to ten
	// minutes. This does not satisfy backlog NFR-05.4.1's one-minute limit.
	AccessTokenLifetime  = 10 * time.Minute
	RefreshTokenLifetime = 30 * 24 * time.Hour
)

func (s *Service) RequestLink(ctx context.Context, email string) error {
	normalizedEmail, err := NormalizeEmail(email)
	if err != nil {
		return err
	}

	user, err := s.deps.Resources.Users.GetByEmail(ctx, normalizedEmail)
	isLogin := err == nil
	if isLogin {
		if user.ID == uuid.Nil {
			return errors.New("found user has no ID")
		}
	} else if !errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("find user by email: %w", err)
	}
	role := models.RoleUser
	if isLogin {
		role = user.Role
	}
	if err := requireAllowedEmailDomain(ctx, s.deps.Resources.DomainPolicy, normalizedEmail, role); err != nil {
		return err
	}

	token, digest, err := randomToken(32)
	if err != nil {
		return fmt.Errorf("generate magic link: %w", err)
	}

	expires := s.cfg.Now().UTC().Add(MagicLinkLifetime)
	challenge := models.AuthToken{
		TokenHash: append([]byte(nil), digest[:]...),
		Email:     normalizedEmail,
		ExpiresAt: expires,
	}
	if isLogin {
		challenge.Purpose = models.TokenPurposeLogin
		challenge.UserID = &user.ID
	} else {
		challenge.Purpose = models.TokenPurposeRegister
	}
	if err := s.deps.Resources.AuthTokens.Create(ctx, &challenge); err != nil {
		return fmt.Errorf("save magic link: %w", err)
	}

	link := s.cfg.FrontendBaseURL
	if isLogin {
		link.Path = strings.TrimRight(link.Path, "/") + "/login"
	} else {
		link.Path = strings.TrimRight(link.Path, "/") + "/register"
	}
	query := link.Query()
	query.Set("token", token)
	link.RawQuery = query.Encode()
	if err := s.sendMagicLinkEmail(ctx, normalizedEmail, link.String()); err != nil {
		return err
	}
	return nil
}

func (s *Service) sendMagicLinkEmail(ctx context.Context, recipient, link string) error {
	err := s.deps.EmailSender.Send(ctx, email.Email{
		To:      []string{recipient},
		Subject: "Your sign-in link",
		HTMLBody: "<p>Use this link to sign in or create an account:</p><p><a href=\"" + html.EscapeString(
			link,
		) + "\">Continue</a></p>",
		TextBody: "Use this link to sign in or create an account:\n" + link,
	})
	if err != nil {
		slog.WarnContext(ctx, "send magic link email failed", "err", err)
		return jwt.ErrUnavailable
	}

	return nil
}

func (s *Service) Login(ctx context.Context, loginToken string) (models.User, jwt.AuthTokens, error) {
	digest, err := digestMagicToken(loginToken)
	if err != nil {
		return models.User{}, jwt.AuthTokens{}, jwt.ErrLoginFailed
	}

	now := s.cfg.Now().UTC()
	var (
		user   models.User
		tokens jwt.AuthTokens
	)
	err = s.deps.WithTransaction(ctx, func(tx Resources) error {
		challenge, err := tx.AuthTokens.Consume(ctx, digest, models.TokenPurposeLogin, now)
		if err != nil {
			return err
		}
		if challenge.UserID == nil {
			return store.ErrChallengeRejected
		}
		storedUser, err := tx.Users.GetByIDForUpdate(ctx, *challenge.UserID)
		if err != nil {
			return err
		}
		if storedUser.ID == uuid.Nil || !storedUser.Role.Valid() {
			return jwt.ErrLoginFailed
		}
		if err := requireAllowedEmailDomain(ctx, tx.DomainPolicy, storedUser.Email, storedUser.Role); err != nil {
			return err
		}
		user = *storedUser
		signed, err := s.createSession(ctx, tx, user, now)
		if err != nil {
			return err
		}
		tokens = signed
		return nil
	})
	if errors.Is(err, store.ErrChallengeRejected) || errors.Is(err, store.ErrNotFound) ||
		errors.Is(err, jwt.ErrLoginFailed) {
		return models.User{}, jwt.AuthTokens{}, jwt.ErrLoginFailed
	}
	if err != nil {
		return models.User{}, jwt.AuthTokens{}, fmt.Errorf("login transaction: %w", err)
	}
	return user, tokens, nil
}

func (s *Service) Register(
	ctx context.Context,
	registrationToken string,
	profile jwt.Profile,
) (models.User, jwt.AuthTokens, error) {
	profile.DisplayName = strings.TrimSpace(profile.DisplayName)
	if profile.DisplayName == "" || utf8.RuneCountInString(profile.DisplayName) > MaxDisplayNameLength {
		return models.User{}, jwt.AuthTokens{}, jwt.ErrInvalidProfile
	}
	phone, ok := normalizedPhoneNumber(profile.PhoneNumber)
	if !ok {
		return models.User{}, jwt.AuthTokens{}, jwt.ErrInvalidProfile
	}
	digest, err := digestMagicToken(registrationToken)
	if err != nil {
		return models.User{}, jwt.AuthTokens{}, jwt.ErrRegistrationFailed
	}
	now := s.cfg.Now().UTC()
	var (
		user   models.User
		tokens jwt.AuthTokens
	)
	err = s.deps.WithTransaction(ctx, func(tx Resources) error {
		challenge, err := tx.AuthTokens.Consume(ctx, digest, models.TokenPurposeRegister, now)
		if err != nil {
			return err
		}
		if err := requireAllowedEmailDomain(ctx, tx.DomainPolicy, challenge.Email, models.RoleUser); err != nil {
			return err
		}
		user = models.User{Email: challenge.Email, DisplayName: profile.DisplayName,
			PhoneNumber: &phone, Role: models.RoleUser}
		provisioner := UserProvisioner{Users: tx.Users, Events: tx.UserCreatedEvents}
		if err := provisioner.Create(ctx, &user); err != nil {
			if errors.Is(err, store.ErrDuplicate) {
				return jwt.ErrAlreadyRegistered
			}
			return err
		}
		// Re-read under the row lock so issuance always signs the current
		// lifecycle state, mirroring login and refresh.
		live, err := tx.Users.GetByIDForUpdate(ctx, user.ID)
		if err != nil {
			return err
		}
		user = *live
		signed, err := s.createSession(ctx, tx, user, now)
		if err != nil {
			return err
		}
		tokens = signed
		return nil
	})
	if errors.Is(err, store.ErrChallengeRejected) {
		return models.User{}, jwt.AuthTokens{}, jwt.ErrRegistrationFailed
	}
	if errors.Is(err, jwt.ErrRegistrationFailed) {
		return models.User{}, jwt.AuthTokens{}, jwt.ErrRegistrationFailed
	}
	if errors.Is(err, jwt.ErrAlreadyRegistered) {
		return models.User{}, jwt.AuthTokens{}, jwt.ErrAlreadyRegistered
	}
	if err != nil {
		return models.User{}, jwt.AuthTokens{}, fmt.Errorf("registration transaction: %w", err)
	}
	return user, tokens, nil
}

func (s *Service) Refresh(ctx context.Context, refreshToken string) (models.User, jwt.AuthTokens, error) {
	now := s.cfg.Now().UTC()
	claims, err := s.deps.TokenCodec.Verify(refreshToken, jwt.RefreshToken, now)
	if err != nil {
		return models.User{}, jwt.AuthTokens{}, jwt.ErrRefreshFailed
	}
	userID, err := parseID(claims.Subject)
	if err != nil {
		return models.User{}, jwt.AuthTokens{}, jwt.ErrRefreshFailed
	}
	sessionID, err := parseID(claims.SessionID)
	if err != nil {
		return models.User{}, jwt.AuthTokens{}, jwt.ErrRefreshFailed
	}
	var (
		user   models.User
		tokens jwt.AuthTokens
	)
	err = s.deps.WithTransaction(ctx, func(tx Resources) error {
		// Lock and re-read the live row so a token minted here can never come
		// from a pre-deletion or pre-role-change snapshot.
		live, err := tx.Users.GetByIDForUpdate(ctx, userID)
		if err != nil {
			return err
		}
		if live.ID != userID || !live.Role.Valid() {
			return store.ErrSessionRejected
		}
		if err := requireAllowedEmailDomain(ctx, tx.DomainPolicy, live.Email, live.Role); err != nil {
			return err
		}
		if err := tx.Sessions.Revoke(ctx, sessionID, sha256.Sum256([]byte(refreshToken)), now); err != nil {
			return err
		}
		user = *live
		tokens, err = s.createSession(ctx, tx, user, now)
		return err
	})
	if errors.Is(err, store.ErrSessionRejected) || errors.Is(err, store.ErrNotFound) {
		return models.User{}, jwt.AuthTokens{}, jwt.ErrRefreshFailed
	}
	if err != nil {
		return models.User{}, jwt.AuthTokens{}, fmt.Errorf("replace refresh session: %w", err)
	}
	return user, tokens, nil
}

// Logout is idempotent. An unrecognizable or already revoked cookie is cleared
// by the HTTP adapter; a valid cookie is revoked before success is returned.
func (s *Service) Logout(ctx context.Context, refreshToken string) error {
	now := s.cfg.Now().UTC()
	claims, err := s.deps.TokenCodec.Verify(refreshToken, jwt.RefreshToken, now)
	if err != nil {
		return nil
	}
	sessionID, err := parseID(claims.SessionID)
	if err != nil {
		return nil
	}
	err = s.deps.Resources.Sessions.Revoke(ctx, sessionID, sha256.Sum256([]byte(refreshToken)), now)
	if errors.Is(err, store.ErrSessionRejected) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("revoke refresh session: %w", err)
	}
	return nil
}

// LogoutAll revokes every session of the refresh cookie's user, signing them
// out of all devices. Unlike Logout, the cookie must belong to a live session,
// so an unauthenticated caller cannot sign anyone out.
func (s *Service) LogoutAll(ctx context.Context, refreshToken string) error {
	now := s.cfg.Now().UTC()
	claims, err := s.deps.TokenCodec.Verify(refreshToken, jwt.RefreshToken, now)
	if err != nil {
		return jwt.ErrRefreshFailed
	}
	userID, err := parseID(claims.Subject)
	if err != nil {
		return jwt.ErrRefreshFailed
	}
	sessionID, err := parseID(claims.SessionID)
	if err != nil {
		return jwt.ErrRefreshFailed
	}
	err = s.deps.WithTransaction(ctx, func(tx Resources) error {
		if err := tx.Sessions.Revoke(ctx, sessionID, sha256.Sum256([]byte(refreshToken)), now); err != nil {
			return err
		}
		_, err := tx.Sessions.RevokeAllForUser(ctx, userID, now)
		return err
	})
	if errors.Is(err, store.ErrSessionRejected) {
		return jwt.ErrRefreshFailed
	}
	if err != nil {
		return fmt.Errorf("revoke all sessions: %w", err)
	}
	return nil
}

func (s *Service) PublicKeys() (jwt.JWKSet, error) {
	return s.deps.TokenCodec.PublicKeys(), nil
}

// DeleteMyAccountResult is the authenticated acceptance result: the original
// immutable request time, the live account snapshot and the replacement
// session's tokens. The access token carries the deleting lifecycle claim.
type DeleteMyAccountResult struct {
	RequestedAt time.Time
	User        models.User
	Tokens      jwt.AuthTokens
}

// DeleteMyAccount accepts permanent deletion for the account that owns the
// refresh cookie. It derives the account from the verified live session and
// accepts no user ID or email. Acceptance is irreversible even with outstanding
// work, so this method never rejects on work state: the caller keeps a
// restricted replacement session until the worker finalizes the account.
//
// A retry against an already-deleting account returns the original request time
// and does not repeat the acceptance transaction or its all-session sweep; it
// only rotates the caller's still-live session.
func (s *Service) DeleteMyAccount(ctx context.Context, refreshToken string) (DeleteMyAccountResult, error) {
	now := s.cfg.Now().UTC()
	claims, err := s.deps.TokenCodec.Verify(refreshToken, jwt.RefreshToken, now)
	if err != nil {
		return DeleteMyAccountResult{}, jwt.ErrRefreshFailed
	}
	userID, err := parseID(claims.Subject)
	if err != nil {
		return DeleteMyAccountResult{}, jwt.ErrRefreshFailed
	}
	sessionID, err := parseID(claims.SessionID)
	if err != nil {
		return DeleteMyAccountResult{}, jwt.ErrRefreshFailed
	}
	user, err := s.deps.Resources.Users.GetByID(ctx, userID)
	if errors.Is(err, store.ErrNotFound) {
		return DeleteMyAccountResult{}, jwt.ErrRefreshFailed
	}
	if err != nil {
		return DeleteMyAccountResult{}, fmt.Errorf("find deletion user: %w", err)
	}
	if user.ID != userID || !user.Role.Valid() {
		return DeleteMyAccountResult{}, jwt.ErrRefreshFailed
	}
	// Reject protected accounts before any session is revoked or any row changes.
	if user.Role.IsAdmin() {
		return DeleteMyAccountResult{}, ErrProtectedAccount
	}
	digest := sha256.Sum256([]byte(refreshToken))
	if user.IsDeleting {
		return s.rotateDeletingSession(ctx, userID, sessionID, digest, now)
	}
	// Authenticate the live session before accepting: a well-formed but revoked
	// or expired refresh cookie must not be able to start deletion. The
	// acceptance transaction re-locks the user, captures these live session ids
	// for revocation, sweeps them and issues the replacement session.
	live, err := s.isLiveSession(ctx, userID, sessionID)
	if err != nil {
		return DeleteMyAccountResult{}, fmt.Errorf("verify deletion session: %w", err)
	}
	if !live {
		return DeleteMyAccountResult{}, jwt.ErrRefreshFailed
	}
	result, err := s.deps.Deletion.Accept(ctx, user, now)
	if errors.Is(err, accountdeletion.ErrDeletingForbidden) {
		return DeleteMyAccountResult{}, ErrProtectedAccount
	}
	if errors.Is(err, accountdeletion.ErrAlreadyDeleted) || errors.Is(err, store.ErrNotFound) {
		return DeleteMyAccountResult{}, jwt.ErrRefreshFailed
	}
	if err != nil {
		return DeleteMyAccountResult{}, fmt.Errorf("accept account deletion: %w", err)
	}
	return DeleteMyAccountResult{RequestedAt: result.RequestedAt, User: result.User, Tokens: result.Tokens}, nil
}

// isLiveSession reports whether sessionID is one of the user's unrevoked
// sessions. It reads without mutating, so acceptance can capture and sweep every
// live session itself for the access-token revocation hook.
func (s *Service) isLiveSession(ctx context.Context, userID, sessionID uuid.UUID) (bool, error) {
	ids, err := s.deps.Resources.Sessions.LiveSessionIDsForUser(ctx, userID)
	if err != nil {
		return false, err
	}
	for _, id := range ids {
		if id == sessionID {
			return true, nil
		}
	}
	return false, nil
}

// rotateDeletingSession handles an idempotent acceptance retry. It re-reads the
// already-deleting account under the row lock so finalization cannot race the
// replacement session, validates the presented session, and issues a new
// restricted token pair. It deliberately leaves the pending job and its request
// time untouched and does not sweep other sessions again.
func (s *Service) rotateDeletingSession(
	ctx context.Context,
	userID, sessionID uuid.UUID,
	digest [32]byte,
	now time.Time,
) (DeleteMyAccountResult, error) {
	var (
		user   models.User
		tokens jwt.AuthTokens
	)
	err := s.deps.WithTransaction(ctx, func(tx Resources) error {
		live, err := tx.Users.GetByIDForUpdate(ctx, userID)
		if err != nil {
			return err
		}
		if !live.IsDeleting || live.DeletionRequestedAt == nil || !live.Role.Valid() || live.Role.IsAdmin() {
			return store.ErrSessionRejected
		}
		if err := tx.Sessions.Revoke(ctx, sessionID, digest, now); err != nil {
			return err
		}
		user = *live
		tokens, err = s.Issue(ctx, tx.Sessions, &user, now)
		return err
	})
	if errors.Is(err, store.ErrSessionRejected) || errors.Is(err, store.ErrNotFound) {
		return DeleteMyAccountResult{}, jwt.ErrRefreshFailed
	}
	if err != nil {
		return DeleteMyAccountResult{}, fmt.Errorf("rotate deleting session: %w", err)
	}
	return DeleteMyAccountResult{RequestedAt: *user.DeletionRequestedAt, User: user, Tokens: tokens}, nil
}

// Issue is the account-deletion SessionIssuer port. Acceptance revokes the
// caller's session and needs a fresh one so the account can finish existing work
// while it deletes; Issue creates that replacement session and signs a token pair
// whose access token carries the deleting lifecycle claim. The refresh token
// never carries is_deleting (the codec rejects it), so the rotated session cannot
// be used to mint a long-lived deleting cookie.
func (s *Service) Issue(
	ctx context.Context,
	sessions accountdeletion.SessionStore,
	user *models.User,
	now time.Time,
) (jwt.AuthTokens, error) {
	placeholderHash := make([]byte, sha256.Size)
	if _, err := rand.Read(placeholderHash); err != nil {
		return jwt.AuthTokens{}, fmt.Errorf("generate session placeholder: %w", err)
	}
	session := models.Session{UserID: user.ID, TokenHash: placeholderHash,
		LastSeenAt: now, ExpiresAt: now.Add(s.cfg.RefreshTokenTTL)}
	if err := sessions.Create(ctx, &session); err != nil {
		return jwt.AuthTokens{}, err
	}
	tokens, err := s.signSessionTokens(*user, session.ID, now, true)
	if err != nil {
		return jwt.AuthTokens{}, err
	}
	digest := sha256.Sum256([]byte(tokens.RefreshToken))
	if err := sessions.UpdateTokenHash(ctx, session.ID, digest[:]); err != nil {
		return jwt.AuthTokens{}, fmt.Errorf("save refresh token hash: %w", err)
	}
	return tokens, nil
}

func (s *Service) createSession(
	ctx context.Context,
	tx Resources,
	user models.User,
	now time.Time,
) (jwt.AuthTokens, error) {
	placeholderHash := make([]byte, sha256.Size)
	if _, err := rand.Read(placeholderHash); err != nil {
		return jwt.AuthTokens{}, fmt.Errorf("generate session placeholder: %w", err)
	}
	session := models.Session{UserID: user.ID, TokenHash: placeholderHash,
		LastSeenAt: now, ExpiresAt: now.Add(s.cfg.RefreshTokenTTL)}
	if err := tx.Sessions.Create(ctx, &session); err != nil {
		return jwt.AuthTokens{}, err
	}
	tokens, err := s.signSessionTokens(user, session.ID, now, user.IsDeleting)
	if err != nil {
		return jwt.AuthTokens{}, err
	}
	digest := sha256.Sum256([]byte(tokens.RefreshToken))
	if err := tx.Sessions.UpdateTokenHash(ctx, session.ID, digest[:]); err != nil {
		return jwt.AuthTokens{}, fmt.Errorf("save refresh token hash: %w", err)
	}
	return tokens, nil
}

func (s *Service) signSessionTokens(
	user models.User,
	sessionID uuid.UUID,
	now time.Time,
	isDeleting bool,
) (jwt.AuthTokens, error) {
	role, ok := jwtRole(user.Role)
	if !ok {
		return jwt.AuthTokens{}, errors.New("user has invalid role")
	}
	accessID, _, err := randomToken(16)
	if err != nil {
		return jwt.AuthTokens{}, fmt.Errorf("generate access token ID: %w", err)
	}
	refreshID, _, err := randomToken(16)
	if err != nil {
		return jwt.AuthTokens{}, fmt.Errorf("generate refresh token ID: %w", err)
	}
	accessExpiry := now.Add(s.cfg.AccessTokenTTL)
	refreshExpiry := now.Add(s.cfg.RefreshTokenTTL)
	subject := user.ID.String()
	sessionSubject := sessionID.String()
	access, err := s.deps.TokenCodec.Sign(jwt.Claims{Type: jwt.AccessToken, Subject: subject,
		SessionID: sessionSubject, Role: role, IsDeleting: isDeleting, IssuedAt: now,
		ExpiresAt: accessExpiry, TokenID: accessID})
	if err != nil {
		return jwt.AuthTokens{}, fmt.Errorf("sign access token: %w", err)
	}
	refresh, err := s.deps.TokenCodec.Sign(jwt.Claims{Type: jwt.RefreshToken, Subject: subject,
		SessionID: sessionSubject, IssuedAt: now, ExpiresAt: refreshExpiry, TokenID: refreshID})
	if err != nil {
		return jwt.AuthTokens{}, fmt.Errorf("sign refresh token: %w", err)
	}
	return jwt.AuthTokens{AccessToken: access, RefreshToken: refresh,
		AccessExpiry: accessExpiry, RefreshExpiry: refreshExpiry}, nil
}

func parseID(value string) (uuid.UUID, error) {
	return uuid.Parse(value)
}

func jwtRole(role models.RoleName) (jwt.Role, bool) {
	switch role {
	case models.RoleSuperAdmin:
		return jwt.RoleSuperAdmin, true
	case models.RoleAdmin:
		return jwt.RoleAdmin, true
	case models.RoleUser:
		return jwt.RoleUser, true
	case models.RoleSuspended:
		return jwt.RoleSuspendedUser, true
	default:
		return "", false
	}
}

// NormalizeEmail trims and lowercases a bare address, or returns jwt.ErrInvalidEmail.
func NormalizeEmail(input string) (string, error) {
	email := strings.ToLower(strings.TrimSpace(input))
	address, err := mail.ParseAddress(email)
	if err != nil || address.Address != email || strings.ContainsAny(email, "\r\n") {
		return "", jwt.ErrInvalidEmail
	}
	return email, nil
}

func requireAllowedEmailDomain(
	ctx context.Context,
	policy EmailDomainPolicy,
	input string,
	role models.RoleName,
) error {
	if role == models.RoleSuperAdmin {
		return nil
	}
	email, err := NormalizeEmail(input)
	if err != nil {
		return ErrEmailDomainNotAllowed
	}
	separator := strings.LastIndexByte(email, '@')
	if separator <= 0 || separator == len(email)-1 {
		return ErrEmailDomainNotAllowed
	}
	allowed, err := policy.IsDomainAllowed(ctx, email[separator+1:])
	if err != nil {
		return fmt.Errorf("check email domain: %w", err)
	}
	if !allowed {
		return ErrEmailDomainNotAllowed
	}
	return nil
}

func randomToken(size int) (string, [32]byte, error) {
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		return "", [32]byte{}, err
	}
	token := base64.RawURLEncoding.EncodeToString(buf)
	return token, sha256.Sum256([]byte(token)), nil
}

func digestMagicToken(token string) ([32]byte, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(decoded) != 32 || base64.RawURLEncoding.EncodeToString(decoded) != token {
		return [32]byte{}, jwt.ErrChallengeRejected
	}
	return sha256.Sum256([]byte(token)), nil
}
