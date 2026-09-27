// Package store holds the GORM persistence adapters. Each adapter is a
// concrete type; the packages that use one declare the small interface they
// need, so no interfaces are defined here.
package store

import "errors"

var (
	ErrNotFound      = errors.New("not found")
	ErrDuplicate     = errors.New("duplicate")
	ErrInvalidDomain = errors.New("invalid domain")
	ErrRoleConflict  = errors.New("role changed concurrently")
	// ErrInUse: the user is still referenced with ON DELETE RESTRICT
	// (e.g. the admin_bootstrap row), so it cannot be deleted.
	ErrInUse = errors.New("user cannot be deleted")
)
