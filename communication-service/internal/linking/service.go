// Package linking connects FoC accounts to Telegram chats through one-time
// codes passed to the bot's /start command.
package linking

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// CodeLifetime is how long a link code can be redeemed.
const CodeLifetime = 10 * time.Minute

var (
	// ErrNotLinked means the user has no linked chat.
	ErrNotLinked = errors.New("telegram is not linked")
	// ErrInvalidCode means the code is unknown, already used or expired.
	ErrInvalidCode = errors.New("link code is invalid or expired")
)

// Link is a user's connected Telegram chat.
type Link struct {
	UserID   uuid.UUID
	ChatID   int64
	Username string
	LinkedAt time.Time
}

// Chat identifies the Telegram chat redeeming a code.
type Chat struct {
	ID       int64
	Username string
}

// Store persists codes and links. Implementations must make each method
// atomic.
type Store interface {
	// ReplaceCode stores a new code for userID and deletes their unused ones.
	ReplaceCode(ctx context.Context, userID uuid.UUID, codeHash []byte, expiresAt, now time.Time) error
	// RedeemCode marks an unused, unexpired code as used and links its user to
	// chat, replacing the user's previous chat and moving chat away from any
	// other user. It returns ErrInvalidCode otherwise.
	RedeemCode(ctx context.Context, codeHash []byte, chat Chat, now time.Time) (Link, error)
	// GetLink returns ErrNotLinked when the user has no chat.
	GetLink(ctx context.Context, userID uuid.UUID) (Link, error)
	// GetLinkByChat returns ErrNotLinked when no user has the chat.
	GetLinkByChat(ctx context.Context, chatID int64) (Link, error)
	DeleteLink(ctx context.Context, userID uuid.UUID) error
	DeleteLinkByChat(ctx context.Context, chatID int64) error
}

// Service applies the linking rules.
type Service struct {
	Store Store
	Now   func() time.Time
}

// CreateCode returns a new single-use code for userID and when it expires.
// The code is 32 URL-safe characters, within Telegram's /start limit.
func (s *Service) CreateCode(ctx context.Context, userID uuid.UUID) (string, time.Time, error) {
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", time.Time{}, fmt.Errorf("generate link code: %w", err)
	}
	code := base64.RawURLEncoding.EncodeToString(raw)
	now := s.Now()
	expiresAt := now.Add(CodeLifetime)
	if err := s.Store.ReplaceCode(ctx, userID, hashCode(code), expiresAt, now); err != nil {
		return "", time.Time{}, fmt.Errorf("save link code: %w", err)
	}
	return code, expiresAt, nil
}

// Redeem links chat to the user who created code.
func (s *Service) Redeem(ctx context.Context, code string, chat Chat) (Link, error) {
	if code == "" {
		return Link{}, ErrInvalidCode
	}
	return s.Store.RedeemCode(ctx, hashCode(code), chat, s.Now())
}

// Get returns the user's link, or ErrNotLinked.
func (s *Service) Get(ctx context.Context, userID uuid.UUID) (Link, error) {
	return s.Store.GetLink(ctx, userID)
}

// GetByChat returns the link for chatID, or ErrNotLinked.
func (s *Service) GetByChat(ctx context.Context, chatID int64) (Link, error) {
	return s.Store.GetLinkByChat(ctx, chatID)
}

// Unlink removes the user's link. It succeeds when nothing is linked.
func (s *Service) Unlink(ctx context.Context, userID uuid.UUID) error {
	return s.Store.DeleteLink(ctx, userID)
}

// UnlinkChat removes whichever user is linked to chatID, for example after
// they block the bot.
func (s *Service) UnlinkChat(ctx context.Context, chatID int64) error {
	return s.Store.DeleteLinkByChat(ctx, chatID)
}

func hashCode(code string) []byte {
	sum := sha256.Sum256([]byte(code))
	return sum[:]
}
