package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/models"
	"github.com/google/uuid"
)

// Transaction persistence and transport alone are not a complete dispatch setup.
func roleServiceWithoutRootDispatches(f *accountFixture, calls *int) *RoleService {
	return &RoleService{
		Users: f,
		WithTransaction: func(_ context.Context, fn func(RoleStore) error) error {
			return fn(RoleStore{Users: f, Admin: f, Dispatches: f})
		},
		Effects: roleEffectsFunc(func(context.Context, models.RoleDispatch) error {
			(*calls)++
			return connect.NewError(connect.CodeFailedPrecondition, errors.New("Order rejected the decision"))
		}),
	}
}

// Missing root persistence must fail before new suspension/reinstatement intent,
// role/audit writes or remote effects, even when transaction persistence exists.
func TestMissingRootDispatchStoreFailsBeforeAdmission(t *testing.T) {
	for _, from := range []models.RoleName{models.RoleUser, models.RoleSuspended} {
		t.Run(string(from), func(t *testing.T) {
			actorID, targetID := uuid.New(), uuid.New()
			f := &accountFixture{users: map[uuid.UUID]models.User{
				actorID:  {ID: actorID, Role: models.RoleAdmin},
				targetID: {ID: targetID, Role: from},
			}}
			to := models.RoleSuspended
			if from == models.RoleSuspended {
				to = models.RoleUser
			}
			calls := 0
			s := roleServiceWithoutRootDispatches(f, &calls)

			if _, err := s.ChangeUserRole(context.Background(), actorID, targetID, to, "explicit decision"); !errors.Is(err, ErrRoleEffectsUnconfigured) {
				t.Fatalf("missing dispatch store: %v", err)
			}
			if calls != 0 || len(f.dispatches) != 0 || len(f.changes) != 0 || f.users[targetID].Role != from {
				t.Fatal("incomplete setup admitted intent, changed role/audit or called Order")
			}
		})
	}
}

// Retry and recovery must leave already durable intent unchanged, not dispatch,
// acknowledge, reject or drop it when root persistence is unavailable.
func TestMissingRootDispatchStorePreservesExistingIntent(t *testing.T) {
	ctx := context.Background()
	paths := []struct {
		name string
		run  func(*RoleService, models.RoleDispatch) error
	}{
		{"role retry", func(s *RoleService, d models.RoleDispatch) error {
			_, err := s.ChangeUserRole(ctx, d.AdminID, d.UserID, d.ToRole, d.Reason)
			return err
		}},
		{"direct apply", func(s *RoleService, d models.RoleDispatch) error {
			_, err := s.applyRoleDispatch(ctx, d)
			return err
		}},
		{"recovery", func(s *RoleService, _ models.RoleDispatch) error {
			return s.RecoverRoleDispatchesOnce(ctx)
		}},
	}
	for _, from := range []models.RoleName{models.RoleUser, models.RoleSuspended} {
		for _, path := range paths {
			t.Run(string(from)+"/"+path.name, func(t *testing.T) {
				actorID, targetID := uuid.New(), uuid.New()
				to := models.RoleSuspended
				if from == models.RoleSuspended {
					to = models.RoleUser
				}
				d := models.RoleDispatch{
					ID:        uuid.New(),
					UserID:    targetID,
					AdminID:   actorID,
					FromRole:  from,
					ToRole:    to,
					Revision:  9,
					Reason:    "retained decision",
					CreatedAt: time.Now().UTC(),
				}
				f := &accountFixture{
					users: map[uuid.UUID]models.User{
						actorID:  {ID: actorID, Role: models.RoleAdmin},
						targetID: {ID: targetID, Role: from},
					},
					dispatches: map[uuid.UUID]models.RoleDispatch{d.ID: d},
				}
				calls := 0
				s := roleServiceWithoutRootDispatches(f, &calls)

				if err := path.run(s, d); !errors.Is(err, ErrRoleEffectsUnconfigured) {
					t.Fatalf("missing dispatch store: %v", err)
				}
				if calls != 0 || len(f.changes) != 0 || f.users[targetID].Role != from || len(f.dispatches) != 1 || f.dispatches[d.ID] != d {
					t.Fatal("incomplete setup dispatched or changed existing intent, role or audit")
				}
			})
		}
	}
}

func TestOrdinaryRoleChangesDoNotRequireRootDispatchStore(t *testing.T) {
	for _, from := range []models.RoleName{models.RoleUser, models.RoleAdmin} {
		t.Run(string(from), func(t *testing.T) {
			actorID, targetID := uuid.New(), uuid.New()
			f := &accountFixture{users: map[uuid.UUID]models.User{
				actorID:  {ID: actorID, Role: models.RoleSuperAdmin},
				targetID: {ID: targetID, Role: from},
			}}
			to := models.RoleAdmin
			if from == models.RoleAdmin {
				to = models.RoleUser
			}
			calls := 0
			s := roleServiceWithoutRootDispatches(f, &calls)

			got, err := s.ChangeUserRole(context.Background(), actorID, targetID, to, "")
			if err != nil || got.Role != to || f.users[targetID].Role != to || len(f.changes) != 1 || len(f.dispatches) != 0 || calls != 0 {
				t.Fatalf("ordinary role change depended on suspension dispatch: %+v %v", got, err)
			}
		})
	}
}
