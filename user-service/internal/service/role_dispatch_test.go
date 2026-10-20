package service

import (
	"context"
	"errors"
	"testing"

	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/models"
	"github.com/google/uuid"
)

type roleEffectsFunc func(context.Context, models.RoleDispatch) error

func (f roleEffectsFunc) ApplyRoleDecision(ctx context.Context, d models.RoleDispatch) error {
	return f(ctx, d)
}

func TestRoleDecisionWaitsForOrderAndRecoversSameIdentity(t *testing.T) {
	ctx := context.Background()
	adminID, userID := uuid.New(), uuid.New()
	f := &accountFixture{users: map[uuid.UUID]models.User{adminID: {ID: adminID, Role: models.RoleAdmin}, userID: {ID: userID, Role: models.RoleUser}}}
	calls := 0
	var dispatched []models.RoleDispatch
	s := &RoleService{Users: f, Dispatches: f, WithTransaction: func(_ context.Context, fn func(RoleStore) error) error {
		return fn(RoleStore{Users: f, Admin: f, Dispatches: f})
	}}
	s.Effects = roleEffectsFunc(func(_ context.Context, d models.RoleDispatch) error {
		calls++
		dispatched = append(dispatched, d)
		if f.users[userID].Role != d.FromRole || len(f.changes) != int(d.Revision)-1 {
			t.Error("User finalized before Order effects")
		}
		if calls == 1 {
			return errors.New("reply lost after Order commit")
		}
		return nil
	})
	if _, err := s.ChangeUserRole(ctx, adminID, userID, models.RoleSuspended, "investigation"); !errors.Is(err, ErrRoleEffectsUnavailable) {
		t.Fatal(err)
	}
	if f.users[userID].Role != models.RoleUser || len(f.changes) != 0 || len(f.dispatches) != 1 {
		t.Fatal("uncertain handoff finalized role or lost durable intent")
	}
	if _, err := s.ChangeUserRole(ctx, adminID, userID, models.RoleSuspended, "different reason"); !errors.Is(err, ErrConcurrentRoleEdit) || calls != 1 {
		t.Fatalf("changed pending intent: %v", err)
	}
	// A replacement service instance retains only dependencies, not request state.
	restarted := *s
	if err := restarted.RecoverRoleDispatchesOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if f.users[userID].Role != models.RoleSuspended || len(f.changes) != 1 || dispatched[0].ID != dispatched[1].ID || dispatched[0].Revision != dispatched[1].Revision {
		t.Fatal("recovery changed identity or failed to audit once")
	}
	if err := restarted.RecoverRoleDispatchesOnce(ctx); err != nil || calls != 2 {
		t.Fatal("completed decision dispatched again")
	}
	if _, err := restarted.ChangeUserRole(ctx, adminID, userID, models.RoleUser, "appeal granted"); err != nil {
		t.Fatal(err)
	}
	if f.users[userID].Role != models.RoleUser || len(f.changes) != 2 || dispatched[2].Revision != 2 {
		t.Fatal("reinstatement did not advance durable version")
	}
}

func TestUnconfiguredRoleEffectsFailBeforeAdmission(t *testing.T) {
	adminID, userID := uuid.New(), uuid.New()
	f := &accountFixture{users: map[uuid.UUID]models.User{adminID: {ID: adminID, Role: models.RoleAdmin}, userID: {ID: userID, Role: models.RoleUser}}}
	s := &RoleService{Users: f, Dispatches: f, WithTransaction: func(_ context.Context, fn func(RoleStore) error) error {
		return fn(RoleStore{Users: f, Admin: f, Dispatches: f})
	}}
	if _, err := s.ChangeUserRole(context.Background(), adminID, userID, models.RoleSuspended, "explicit decision"); !errors.Is(err, ErrRoleEffectsUnconfigured) {
		t.Fatal(err)
	}
	if len(f.dispatches) != 0 || len(f.changes) != 0 || f.users[userID].Role != models.RoleUser {
		t.Fatal("unconfigured transport admitted a suspension")
	}
}
