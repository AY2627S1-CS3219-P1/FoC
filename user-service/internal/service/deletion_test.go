package service

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/models"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/store"
	"github.com/google/uuid"
)

type deletionUsers struct {
	rows map[uuid.UUID]models.User
	lockedActor *models.User
}

func (u *deletionUsers) GetByID(_ context.Context, id uuid.UUID) (*models.User, error) {
	row, ok := u.rows[id]
	if !ok {
		return nil, store.ErrNotFound
	}
	return &row, nil
}

func (u *deletionUsers) GetByEmail(context.Context, string) (*models.User, error) {
	return nil, store.ErrNotFound
}

func (u *deletionUsers) GetByIDForUpdate(ctx context.Context, id uuid.UUID) (*models.User, error) {
	if u.lockedActor != nil && u.lockedActor.ID == id {
		return u.lockedActor, nil
	}
	return u.GetByID(ctx, id)
}

type deletionCheck func(context.Context, uuid.UUID) (OutstandingWork, error)

func (f deletionCheck) GetUserOutstandingWork(ctx context.Context, id uuid.UUID) (OutstandingWork, error) {
	return f(ctx, id)
}

type deletionAccount func(context.Context, *models.User, models.UserDeletedOutbox) error

func (f deletionAccount) HardDeleteAccount(ctx context.Context, u *models.User, fact models.UserDeletedOutbox) error {
	return f(ctx, u, fact)
}

func deletionFixture() (*DeletionService, *deletionUsers, uuid.UUID, uuid.UUID) {
	actor, target := uuid.New(), uuid.New()
	users := &deletionUsers{rows: map[uuid.UUID]models.User{
		actor: {BaseModel: models.BaseModel{ID: actor}, Role: models.RoleAdmin},
		target: {BaseModel: models.BaseModel{ID: target}, Role: models.RoleUser},
	}}
	return &DeletionService{Users: users}, users, actor, target
}

func TestDeletionStopsAtFirstOutstandingOrError(t *testing.T) {
	for _, tc := range []struct {
		name string
		failAt string
		busy bool
		want []string
	}{
		{"order unavailable", "Order", false, []string{"Order"}},
		{"order busy", "Order", true, []string{"Order"}},
		{"credit unavailable", "Credit", false, []string{"Order", "Credit"}},
		{"credit busy", "Credit", true, []string{"Order", "Credit"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _, actor, target := deletionFixture()
			var calls []string
			check := func(name string) deletionCheck {
				return func(ctx context.Context, id uuid.UUID) (OutstandingWork, error) {
					if _, ok := ctx.Deadline(); !ok || id != target {
						t.Fatal("check must have a deadline and the selected UUID")
					}
					calls = append(calls, name)
					if name != tc.failAt {
						return OutstandingWork{}, nil
					}
					if tc.busy {
						return OutstandingWork{HasOutstandingWork: true, Reasons: []string{"active work"}}, nil
					}
					return OutstandingWork{}, errors.New("private database error")
				}
			}
			s.Order, s.Credit = check("Order"), check("Credit")
			s.WithTransaction = func(context.Context, func(DeletionStore) error) error {
				t.Fatal("failed precondition entered a write transaction")
				return nil
			}
			if err := s.DeleteUser(context.Background(), actor, target); err == nil ||
				!strings.Contains(err.Error(), tc.failAt + " Service") || strings.Contains(err.Error(), "private database") {
				t.Fatalf("failed check lost service context or exposed private error: %v", err)
			}
			if !reflect.DeepEqual(calls, tc.want) {
				t.Fatalf("calls = %v, want %v", calls, tc.want)
			}
		})
	}
}

func TestCancelledDeletionDoesNotEnterWriteTransaction(t *testing.T) {
	s, _, actor, target := deletionFixture()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s.Order = deletionCheck(func(context.Context, uuid.UUID) (OutstandingWork, error) { return OutstandingWork{}, nil })
	s.Credit = deletionCheck(func(context.Context, uuid.UUID) (OutstandingWork, error) {
		t.Fatal("continued after cancellation")
		return OutstandingWork{}, nil
	})
	s.WithTransaction = func(context.Context, func(DeletionStore) error) error {
		t.Fatal("cancelled deletion entered a write transaction")
		return nil
	}
	if err := s.DeleteUser(ctx, actor, target); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation = %v", err)
	}
}

func TestDeletionPermissionsAndLiveRoleRecheck(t *testing.T) {
	for _, tc := range []struct {
		name string
		actorRole, targetRole models.RoleName
		self bool
	}{
		{"ordinary", models.RoleUser, models.RoleUser, false},
		{"suspended", models.RoleSuspended, models.RoleUser, false},
		{"self", models.RoleAdmin, models.RoleAdmin, true},
		{"super admin protected", models.RoleSuperAdmin, models.RoleSuperAdmin, false},
		{"admin cannot delete admin", models.RoleAdmin, models.RoleAdmin, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, users, actor, target := deletionFixture()
			a, b := users.rows[actor], users.rows[target]
			a.Role, b.Role = tc.actorRole, tc.targetRole
			users.rows[actor], users.rows[target] = a, b
			if tc.self {
				target = actor
			}
			// Missing readers would yield Unavailable if authorization did not run first.
			if err := s.DeleteUser(context.Background(), actor, target); !errors.Is(err, ErrPermissionDenied) {
				t.Fatalf("error = %v, want permission denied", err)
			}
		})
	}

	s, users, actor, target := deletionFixture()
	demoted := users.rows[actor]
	demoted.Role = models.RoleUser
	users.lockedActor = &demoted
	clean := deletionCheck(func(context.Context, uuid.UUID) (OutstandingWork, error) { return OutstandingWork{}, nil })
	s.Order, s.Credit = clean, clean
	s.WithTransaction = func(ctx context.Context, operation func(DeletionStore) error) error {
		return operation(DeletionStore{Users: users, Accounts: deletionAccount(func(context.Context, *models.User, models.UserDeletedOutbox) error {
			t.Fatal("stale role authorized a hard delete")
			return nil
		})})
	}
	if err := s.DeleteUser(context.Background(), actor, target); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("live role check = %v", err)
	}
}

func TestDeletionPassesStableFactToTransaction(t *testing.T) {
	s, users, actor, target := deletionFixture()
	a := users.rows[actor]
	a.Role = models.RoleSuperAdmin
	users.rows[actor] = a
	b := users.rows[target]
	b.Role = models.RoleAdmin
	users.rows[target] = b
	now := time.Date(2026, 11, 9, 3, 0, 0, 0, time.UTC)
	s.Now = func() time.Time { return now }
	clean := deletionCheck(func(context.Context, uuid.UUID) (OutstandingWork, error) { return OutstandingWork{}, nil })
	s.Order, s.Credit = clean, clean
	called := false
	s.WithTransaction = func(ctx context.Context, operation func(DeletionStore) error) error {
		return operation(DeletionStore{Users: users, Accounts: deletionAccount(func(_ context.Context, u *models.User, f models.UserDeletedOutbox) error {
			called = true
			if u.ID != target || f.UserID != target || f.EventID == uuid.Nil || f.DeletionRequestID == uuid.Nil || !f.DeletedAt.Equal(now) {
				t.Fatalf("invalid deletion fact: %+v", f)
			}
			return nil
		})})
	}
	if err := s.DeleteUser(context.Background(), actor, target); err != nil || !called {
		t.Fatalf("delete eligible admin: called=%v error=%v", called, err)
	}
}
