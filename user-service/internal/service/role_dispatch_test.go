package service

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"connectrpc.com/connect"

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

// A definite rejection is not an uncertain commit. Keep its identity for
// reconciliation, without finalizing User or retrying it each recovery scan.
func TestRejectedRoleDecisionIsVisibleAndNotRetried(t *testing.T) {
	for _, code := range []connect.Code{connect.CodeInvalidArgument, connect.CodeFailedPrecondition, connect.CodeAborted, connect.CodeAlreadyExists} {
		t.Run(code.String(), func(t *testing.T) {
			ctx := context.Background()
			adminID, userID := uuid.New(), uuid.New()
			f := &accountFixture{users: map[uuid.UUID]models.User{
				adminID: {ID: adminID, Role: models.RoleAdmin},
				userID:  {ID: userID, Role: models.RoleUser},
			}}
			calls := 0
			s := &RoleService{
				Users:      f,
				Dispatches: f,
				WithTransaction: func(_ context.Context, fn func(RoleStore) error) error {
					return fn(RoleStore{Users: f, Admin: f, Dispatches: f})
				},
				Effects: roleEffectsFunc(func(context.Context, models.RoleDispatch) error {
					calls++
					return connect.NewError(code, errors.New("definite rejection"))
				}),
			}

			if _, err := s.ChangeUserRole(ctx, adminID, userID, models.RoleSuspended, "explicit reason"); !errors.Is(err, ErrRoleEffectsRejected) {
				t.Fatalf("rejection hidden: %v", err)
			}
			pending, err := f.PendingRoleDispatch(ctx, userID)
			if err != nil || pending == nil || pending.RejectedAt == nil {
				t.Fatalf("lost rejection: %+v %v", pending, err)
			}

			identity := pending.ID
			for range 3 {
				if err := s.RecoverRoleDispatchesOnce(ctx); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.ChangeUserRole(ctx, adminID, userID, models.RoleSuspended, "explicit reason"); !errors.Is(err, ErrRoleEffectsRejected) {
				t.Fatalf("repeat rejection hidden: %v", err)
			}
			if _, err := s.ChangeUserRole(ctx, adminID, userID, models.RoleUser, "replace"); err == nil {
				t.Fatal("rejected intent silently replaced")
			}
			pending, _ = f.PendingRoleDispatch(ctx, userID)
			if pending == nil {
				t.Fatal("lost rejected decision")
			}
			if calls != 1 || pending.ID != identity || len(f.dispatches) != 1 || len(f.changes) != 0 || f.users[userID].Role != models.RoleUser {
				t.Fatal("rejected intent retried, replaced or finalized")
			}
		})
	}
}

// A previously loaded recovery item must not send a saved reinstatement after
// its target disappears. An already sent suspension may keep its Order fence.
func TestDeletedRoleTargetIsNotDispatchedOrFinalized(t *testing.T) {
	for _, deleteDuringCall := range []bool{false, true} {
		t.Run(fmt.Sprint(deleteDuringCall), func(t *testing.T) {
			ctx := context.Background()
			adminID, userID := uuid.New(), uuid.New()
			f := &accountFixture{users: map[uuid.UUID]models.User{
				adminID: {ID: adminID, Role: models.RoleAdmin},
				userID:  {ID: userID, Role: models.RoleUser},
			}}
			d := models.RoleDispatch{
				ID:       uuid.New(),
				UserID:   userID,
				AdminID:  adminID,
				FromRole: models.RoleUser,
				ToRole:   models.RoleSuspended,
				Revision: 1,
				Reason:   "explicit suspension",
			}
			f.dispatches = map[uuid.UUID]models.RoleDispatch{d.ID: d}
			calls := 0
			s := &RoleService{Users: f, Dispatches: f, WithTransaction: func(_ context.Context, fn func(RoleStore) error) error {
				return fn(RoleStore{Users: f, Admin: f, Dispatches: f})
			}, Effects: roleEffectsFunc(func(context.Context, models.RoleDispatch) error {
				calls++
				delete(f.users, userID)
				return nil
			})}
			if !deleteDuringCall {
				delete(f.users, userID)
				d.FromRole, d.ToRole = models.RoleSuspended, models.RoleUser
			}
			if _, err := s.applyRoleDispatch(ctx, d); !errors.Is(err, ErrUserNotFound) {
				t.Fatalf("deleted target finalized: %v", err)
			}
			expected := 0
			if deleteDuringCall {
				expected = 1
			}
			if calls != expected || f.dispatches[d.ID].AppliedAt != nil || len(f.changes) != 0 {
				t.Fatal("deleted intent sent, acknowledged or audited")
			}
		})
	}
}
