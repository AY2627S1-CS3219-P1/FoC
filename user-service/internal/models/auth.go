package models

import (
	"time"

	"github.com/google/uuid"
)

type TokenPurpose string

const (
	TokenPurposeRegister TokenPurpose = "register"
	TokenPurposeLogin    TokenPurpose = "login"
)

// MagicLinkTTL is the validity window for register/login links (U1.2.2, U2.1.2).
const MagicLinkTTL = 10 * time.Minute

// AllowedEmailDomain is the admin-edited registration whitelist (U1.1.2).
type AllowedEmailDomain struct {
	ID        uuid.UUID  `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	Domain    string     `gorm:"type:citext;not null;uniqueIndex"`
	CreatedBy *uuid.UUID `gorm:"type:uuid"`
	CreatedAt time.Time
}

// TableName maps AllowedEmailDomain to the allowed_email_domains table for GORM.
func (AllowedEmailDomain) TableName() string { return "allowed_email_domains" }

// AuthToken is a magic link. TokenHash = sha256(raw token); the raw token only
// ever exists in the emailed URL.
type AuthToken struct {
	ID          uuid.UUID    `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	TokenHash   []byte       `gorm:"type:bytea;not null;uniqueIndex"`
	Purpose     TokenPurpose `gorm:"size:16;not null"`
	Email       string       `gorm:"type:citext;not null"`
	UserID      *uuid.UUID   `gorm:"type:uuid"` // nil for registration links
	RequestedIP *string      `gorm:"type:inet"` // textual IP
	CreatedAt   time.Time
	ExpiresAt   time.Time `gorm:"not null"`
	UsedAt      *time.Time
}

// TableName maps AuthToken to the auth_tokens table for GORM.
func (AuthToken) TableName() string { return "auth_tokens" }

// Session is an opaque server-side session (U2.2). TokenHash = sha256(cookie value).
type Session struct {
	ID         uuid.UUID `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	TokenHash  []byte    `gorm:"type:bytea;not null;uniqueIndex"`
	UserID     uuid.UUID `gorm:"type:uuid;not null;index"`
	UserAgent  *string   `gorm:"size:512"`
	IP         *string   `gorm:"type:inet"` // textual IP
	CreatedAt  time.Time
	LastSeenAt time.Time `gorm:"not null;default:now()"`
	ExpiresAt  time.Time `gorm:"not null"`
	RevokedAt  *time.Time
}

// TableName maps Session to the sessions table for GORM.
func (Session) TableName() string { return "sessions" }

// IsActive reports whether the session is unrevoked and t is strictly before
// its expiry. It does not check the user's role.
func (s *Session) IsActive(t time.Time) bool {
	return s.RevokedAt == nil && t.Before(s.ExpiresAt)
}
