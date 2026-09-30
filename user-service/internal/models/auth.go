// Allowed registration domains, magic-link tokens and sessions.

package models

import (
	"time"

	"gorm.io/gorm"
)

type TokenPurpose string

const (
	TokenPurposeRegister TokenPurpose = "register"
	TokenPurposeLogin    TokenPurpose = "login"
)

const MagicLinkTTL = 10 * time.Minute

type AllowedEmailDomain struct {
	gorm.Model
	Userstamps
	Domain string
}

type AuthToken struct {
	gorm.Model
	Userstamps
	TokenHash   []byte
	Purpose     TokenPurpose
	Email       string
	UserID      *uint
	RequestedIP *string
	ExpiresAt   time.Time
	UsedAt      *time.Time
}

type Session struct {
	gorm.Model
	Userstamps
	TokenHash  []byte
	UserID     uint
	UserAgent  *string
	IP         *string
	LastSeenAt time.Time `gorm:"default:now()"`
	ExpiresAt  time.Time
	RevokedAt  *time.Time
}

func (s *Session) IsActive(t time.Time) bool {
	return s.RevokedAt == nil && t.Before(s.ExpiresAt)
}
