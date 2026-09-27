// Package store holds the GORM persistence adapters. Each adapter is a
// concrete type; the packages that use one declare the small interface they
// need, so no interfaces are defined here.
package store

import (
	"context"
	"errors"

	"gorm.io/gorm"
)

var (
	ErrNotFound          = errors.New("not found")
	ErrDuplicate         = errors.New("duplicate")
	ErrInvalidDomain     = errors.New("invalid domain")
	ErrRoleConflict      = errors.New("role changed concurrently")
	ErrChallengeRejected = errors.New("challenge invalid, expired, or used")
	ErrSessionRejected   = errors.New("session invalid, expired, or revoked")
	// ErrInUse: the user is still referenced with ON DELETE RESTRICT
	// (e.g. the admin_bootstrap row), so it cannot be deleted.
	ErrInUse = errors.New("user cannot be deleted")
)

type Store struct {
	*Users
	*Admin
	*AuthTokens
	*Sessions

	db *gorm.DB
}

func New(db *gorm.DB) *Store {
	return &Store{
		Users:      NewUsers(db),
		Admin:      NewAdmin(db),
		AuthTokens: NewAuthTokens(db),
		Sessions:   NewSessions(db),
		db:         db,
	}
}

// WithTransaction runs operation with every embedded adapter bound to one
// database transaction. Returning an error rolls the transaction back.
func (s *Store) WithTransaction(ctx context.Context, operation func(*Store) error) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return operation(New(tx))
	})
}
