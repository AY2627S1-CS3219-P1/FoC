// Allowed registration domains, magic-link tokens and sessions.

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

const MagicLinkTTL = 10 * time.Minute

type AllowedEmailDomain struct {
	BaseModel
	Userstamps
	Domain string
}

type AuthToken struct {
	BaseModel
	Userstamps
	TokenHash   []byte
	Purpose     TokenPurpose
	Email       string
	UserID      *uuid.UUID
	RequestedIP *string
	ExpiresAt   time.Time
	UsedAt      *time.Time
}

type Session struct {
	BaseModel
	Userstamps
	TokenHash  []byte
	UserID     uuid.UUID
	UserAgent  *string
	IP         *string
	LastSeenAt time.Time `gorm:"default:now()"`
	ExpiresAt  time.Time
	RevokedAt  *time.Time
}

func (s *Session) IsActive(t time.Time) bool {
	return s.RevokedAt == nil && t.Before(s.ExpiresAt)
}
