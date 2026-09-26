package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/google/uuid"

	"user-service/internal/apperr"
	"user-service/internal/clock"
	"user-service/internal/config"
	"user-service/internal/mail"
	"user-service/internal/models"
	"user-service/internal/validate"
)

// Errors shown to the client (U1.2.8, U2.1.6, U1.3, NFR-13).
var (
	ErrRegistrationFailed = apperr.New(http.StatusBadRequest, "registration_link_invalid",
		"Registration verification failed: the link is invalid, expired or already used. Request a new one.")
	ErrLoginFailed = apperr.New(http.StatusBadRequest, "login_link_invalid",
		"Login failed: the link is invalid, expired or already used. Request a new one.")
	ErrEmailRegistered = apperr.New(http.StatusConflict, "email_registered",
		"An account with this email already exists. Log in instead.")
	ErrTooManyLinks = apperr.New(http.StatusTooManyRequests, "too_many_requests",
		"Too many links requested. Wait 10 minutes and try again.")
	ErrMailUnavailable = apperr.New(http.StatusServiceUnavailable, "mail_unavailable",
		"We couldn't send the email right now. Please try again shortly.")
)

const (
	rateWindow    = 10 * time.Minute
	touchInterval = 5 * time.Minute
)

// ClientMeta is recorded on tokens and sessions.
type ClientMeta struct {
	IP        string
	UserAgent string
}

// ProfileInput is collected when completing registration (U1.2.3, U1.4).
type ProfileInput struct {
	DisplayName    string
	Description    string
	TelegramHandle *string
	PhoneNumber    *string
}

type Service struct {
	repo      Repository
	mailer    mail.Mailer
	clock     clock.Clock
	cfg       config.AuthConfig
	log       *slog.Logger
	asyncMail bool
}

// NewService builds the auth service. With asyncMail, emails are sent in the
// background so a slow provider doesn't block the request (failures are
// logged, never the token).
func NewService(repo Repository, m mail.Mailer, clk clock.Clock, cfg config.AuthConfig, log *slog.Logger, asyncMail bool) *Service {
	return &Service{repo: repo, mailer: m, clock: clk, cfg: cfg, log: log, asyncMail: asyncMail}
}

// SessionTTL exposes the configured session lifetime.
func (s *Service) SessionTTL() time.Duration { return s.cfg.SessionTTL }

// RequestRegistration emails a 10-minute registration link (U1.1, U1.2.1).
func (s *Service) RequestRegistration(ctx context.Context, rawEmail string, meta ClientMeta) error {
	f := apperr.Fields{}
	email := validate.Email(f, "email", rawEmail)
	if err := f.Err(); err != nil {
		return err
	}
	ok, err := s.repo.DomainAllowed(ctx, validate.EmailDomain(email))
	if err != nil {
		return err
	}
	if !ok {
		return apperr.Invalid("email", "registration is limited to approved email domains; use your school email")
	}
	existing, err := s.repo.UserByEmail(ctx, email)
	if err != nil {
		return err
	}
	if existing != nil {
		return ErrEmailRegistered
	}
	if err := s.checkRate(ctx, email, meta.IP); err != nil {
		return err
	}
	raw, err := s.issue(ctx, models.TokenPurposeRegister, email, nil, meta)
	if err != nil {
		return err
	}
	return s.deliver(ctx, mail.Message{
		To:      email,
		Subject: "Complete your registration",
		Body: "Click the link below to finish creating your account. It expires in 10 minutes.\n\n" +
			s.link("/register/verify", raw) + "\n\nIf you didn't request this, ignore this email.",
	})
}

// CompleteRegistration consumes the link, creates the user and logs them in.
// Returns the raw session token for the cookie.
func (s *Service) CompleteRegistration(ctx context.Context, rawToken string, in ProfileInput, meta ClientMeta) (*models.User, string, *models.Session, error) {
	// Validate first so a typo doesn't burn the single-use link.
	f := apperr.Fields{}
	u := &models.User{
		DisplayName:    validate.DisplayName(f, "display_name", in.DisplayName),
		Description:    validate.Description(f, "description", in.Description),
		TelegramHandle: validate.Telegram(f, "telegram_handle", in.TelegramHandle),
		PhoneNumber:    validate.Phone(f, "phone_number", in.PhoneNumber),
	}
	// U1.4: at least one contact method.
	if u.TelegramHandle == nil && u.PhoneNumber == nil && f["telegram_handle"] == "" && f["phone_number"] == "" {
		f.Add("telegram_handle", "provide a Telegram handle or a phone number")
	}
	if err := f.Err(); err != nil {
		return nil, "", nil, err
	}

	hash, ok := hashToken(rawToken)
	if !ok {
		return nil, "", nil, ErrRegistrationFailed
	}
	rawSess, sess, err := s.newSession(meta)
	if err != nil {
		return nil, "", nil, err
	}
	switch err := s.repo.Register(ctx, hash, s.clock.Now(), u, sess); {
	case errors.Is(err, errLinkInvalid):
		return nil, "", nil, ErrRegistrationFailed
	case errors.Is(err, errEmailTaken):
		return nil, "", nil, ErrEmailRegistered
	case err != nil:
		return nil, "", nil, err
	}
	return u, rawSess, sess, nil
}

// RequestLogin emails a login link if the account exists (U2.1). Always
// succeeds for well-formed emails so it can't be used to probe accounts.
func (s *Service) RequestLogin(ctx context.Context, rawEmail string, meta ClientMeta) error {
	f := apperr.Fields{}
	email := validate.Email(f, "email", rawEmail)
	if err := f.Err(); err != nil {
		return err
	}
	if err := s.checkRate(ctx, email, meta.IP); err != nil {
		return err
	}
	u, err := s.repo.UserByEmail(ctx, email)
	if err != nil || u == nil {
		return err
	}
	// Issuing a new link does not invalidate older ones (U2.1.7).
	raw, err := s.issue(ctx, models.TokenPurposeLogin, u.Email, &u.ID, meta)
	if err != nil {
		return err
	}
	return s.deliver(ctx, mail.Message{
		To:      u.Email,
		Subject: "Your login link",
		Body: "Click the link below to log in. It expires in 10 minutes and works once.\n\n" +
			s.link("/login/verify", raw) + "\n\nIf you didn't request this, ignore this email.",
	})
}

// VerifyLogin consumes a login link and opens a session (U2.1.1).
func (s *Service) VerifyLogin(ctx context.Context, rawToken string, meta ClientMeta) (*models.User, string, *models.Session, error) {
	hash, ok := hashToken(rawToken)
	if !ok {
		return nil, "", nil, ErrLoginFailed
	}
	rawSess, sess, err := s.newSession(meta)
	if err != nil {
		return nil, "", nil, err
	}
	u, err := s.repo.Login(ctx, hash, s.clock.Now(), sess)
	if errors.Is(err, errLinkInvalid) {
		return nil, "", nil, ErrLoginFailed
	}
	if err != nil {
		return nil, "", nil, err
	}
	return u, rawSess, sess, nil
}

// Authenticate resolves a session cookie. ok=false means no valid session.
func (s *Service) Authenticate(ctx context.Context, rawToken string) (Principal, bool, error) {
	hash, ok := hashToken(rawToken)
	if !ok {
		return Principal{}, false, nil
	}
	now := s.clock.Now()
	sess, u, err := s.repo.ActiveSession(ctx, hash, now)
	if err != nil || sess == nil {
		return Principal{}, false, err
	}
	if now.Sub(sess.LastSeenAt) > touchInterval {
		if err := s.repo.TouchSession(ctx, sess.ID, now); err != nil {
			s.log.WarnContext(ctx, "touch session failed", "err", err)
		}
	}
	return Principal{UserID: u.ID, Role: u.Role, SessionID: sess.ID}, true, nil
}

// Logout revokes the caller's current session (U2.2).
func (s *Service) Logout(ctx context.Context, p Principal) error {
	return s.repo.RevokeSession(ctx, p.SessionID, s.clock.Now())
}

// LogoutAll revokes every session of the caller (U2.2.1).
func (s *Service) LogoutAll(ctx context.Context, p Principal) (int64, error) {
	return s.repo.RevokeUserSessions(ctx, p.UserID, s.clock.Now())
}

// ---- internals ----

func (s *Service) checkRate(ctx context.Context, email, ip string) error {
	byEmail, byIP, err := s.repo.RecentLinkCounts(ctx, email, ip, s.clock.Now().Add(-rateWindow))
	if err != nil {
		return err
	}
	if byEmail >= int64(s.cfg.LinkLimitPerEmail) || (ip != "" && byIP >= int64(s.cfg.LinkLimitPerIP)) {
		return ErrTooManyLinks
	}
	return nil
}

func (s *Service) issue(ctx context.Context, p models.TokenPurpose, email string, userID *uuid.UUID, meta ClientMeta) (string, error) {
	raw, hash, err := newToken()
	if err != nil {
		return "", err
	}
	now := s.clock.Now()
	t := &models.AuthToken{
		TokenHash: hash, Purpose: p, Email: email, UserID: userID,
		RequestedIP: strPtr(meta.IP), CreatedAt: now, ExpiresAt: now.Add(models.MagicLinkTTL),
	}
	if err := s.repo.CreateToken(ctx, t); err != nil {
		return "", err
	}
	return raw, nil
}

func (s *Service) newSession(meta ClientMeta) (string, *models.Session, error) {
	raw, hash, err := newToken()
	if err != nil {
		return "", nil, err
	}
	now := s.clock.Now()
	ua := meta.UserAgent
	if len(ua) > 512 {
		ua = ua[:512]
	}
	return raw, &models.Session{
		TokenHash: hash, UserAgent: strPtr(ua), IP: strPtr(meta.IP),
		CreatedAt: now, LastSeenAt: now, ExpiresAt: now.Add(s.cfg.SessionTTL),
	}, nil
}

func (s *Service) link(path, raw string) string {
	return s.cfg.AppBaseURL + path + "?token=" + url.QueryEscape(raw)
}

func (s *Service) deliver(ctx context.Context, m mail.Message) error {
	if s.asyncMail {
		go func() {
			ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			if err := s.mailer.Send(ctx, m); err != nil {
				s.log.Error("send mail failed", "subject", m.Subject, "err", err)
			}
		}()
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := s.mailer.Send(ctx, m); err != nil {
		s.log.ErrorContext(ctx, "send mail failed", "subject", m.Subject, "err", fmt.Sprint(err))
		return ErrMailUnavailable
	}
	return nil
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
