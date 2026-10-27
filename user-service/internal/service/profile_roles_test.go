package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/models"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/store"
	"github.com/google/uuid"
)

type accountFixture struct {
	users        map[uuid.UUID]models.User
	changes      []models.RoleChange
	writeErr     error
	profileError error
}

func (f *accountFixture) GetByID(_ context.Context, id uuid.UUID) (*models.User, error) {
	u, ok := f.users[id]
	if !ok {
		return nil, store.ErrNotFound
	}
	return &u, nil
}

func (f *accountFixture) GetByIDForUpdate(ctx context.Context, id uuid.UUID) (*models.User, error) {
	return f.GetByID(ctx, id)
}

func (f *accountFixture) GetByEmail(_ context.Context, email string) (*models.User, error) {
	for _, u := range f.users {
		if u.Email == email {
			return &u, nil
		}
	}
	return nil, store.ErrNotFound
}

func (f *accountFixture) UpdateActiveProfile(_ context.Context, u *models.User) error {
	if f.profileError != nil {
		return f.profileError
	}
	current, ok := f.users[u.ID]
	if !ok {
		return store.ErrNotFound
	}
	if current.Role == models.RoleSuspended {
		return store.ErrRoleConflict
	}
	current.DisplayName = u.DisplayName
	current.Description = u.Description
	current.TelegramHandle = u.TelegramHandle
	current.PhoneNumber = u.PhoneNumber
	f.users[u.ID] = current
	return nil
}

func (f *accountFixture) ChangeRole(_ context.Context, change *models.RoleChange) error {
	if f.writeErr != nil {
		return f.writeErr
	}
	u, ok := f.users[change.UserID]
	if !ok || u.Role != change.FromRole {
		return store.ErrRoleConflict
	}
	u.Role = change.ToRole
	f.users[u.ID] = u
	f.changes = append(f.changes, *change)
	return nil
}

func ptr(value string) *string { return &value }

func TestProfileFullReplacementAndSuspension(t *testing.T) {
	id := uuid.New()
	f := &accountFixture{users: map[uuid.UUID]models.User{id: {
		ID: id, Email: "person@example.com", DisplayName: "Before", Description: "Old",
		TelegramHandle: ptr("old_handle"), PhoneNumber: ptr("+123"), Role: models.RoleUser,
	}}}
	s := &ProfileService{Users: f}
	got, err := s.UpdateMyProfile(context.Background(), id, ProfileInput{DisplayName: " New ", Description: ""})
	if err != nil || got.DisplayName != "New" || got.Description != "" || got.TelegramHandle != nil || got.PhoneNumber != nil {
		t.Fatalf("full replacement = %+v, %v", got, err)
	}
	if got.Email != "person@example.com" || got.Role != models.RoleUser || got.ID != id {
		t.Fatalf("protected fields changed: %+v", got)
	}
	if _, err := s.UpdateMyProfile(context.Background(), id, ProfileInput{DisplayName: "  "}); !errors.Is(err, ErrInvalidProfile) {
		t.Fatalf("blank name: %v", err)
	}
	validName := strings.Repeat("x", MaxDisplayNameLength)
	got, err = s.UpdateMyProfile(context.Background(), id, ProfileInput{DisplayName: " " + validName + " ",
		TelegramHandle: ptr(" new_handle "), PhoneNumber: ptr(" +12345 ")})
	if err != nil || got.DisplayName != validName || got.TelegramHandle == nil || *got.TelegramHandle != "new_handle" ||
		got.PhoneNumber == nil || *got.PhoneNumber != "+12345" {
		t.Fatalf("normalized 100-character profile: %+v, %v", got, err)
	}
	if _, err := s.UpdateMyProfile(context.Background(), id, ProfileInput{DisplayName: "Valid", TelegramHandle: ptr("@invalid")}); !errors.Is(err, ErrInvalidProfile) {
		t.Fatalf("invalid telegram handle: %v", err)
	}
	if _, err := s.UpdateMyProfile(context.Background(), id, ProfileInput{DisplayName: "Valid", PhoneNumber: ptr(strings.Repeat("1", 21))}); !errors.Is(err, ErrInvalidProfile) {
		t.Fatalf("long phone number: %v", err)
	}
	if _, err := s.UpdateMyProfile(context.Background(), id, ProfileInput{DisplayName: strings.Repeat("x", 101)}); !errors.Is(err, ErrInvalidProfile) {
		t.Fatalf("long name: %v", err)
	}
	u := f.users[id]
	u.Role = models.RoleSuspended
	f.users[id] = u
	if _, err := s.GetMyProfile(context.Background(), id); err != nil {
		t.Fatalf("suspended read: %v", err)
	}
	if _, err := s.UpdateMyProfile(context.Background(), id, ProfileInput{DisplayName: "Other"}); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("suspended update: %v", err)
	}
}

func TestRoleChangePolicyAndReasons(t *testing.T) {
	cases := []struct {
		name    string
		actor   models.RoleName
		target  models.RoleName
		to      models.RoleName
		reason  string
		self    bool
		wantErr error
	}{
		{"super promotes", models.RoleSuperAdmin, models.RoleUser, models.RoleAdmin, "", false, nil},
		{"super demotes admin", models.RoleSuperAdmin, models.RoleAdmin, models.RoleUser, "", false, nil},
		{"super reinstates to admin", models.RoleSuperAdmin, models.RoleSuspended, models.RoleAdmin, "appeal granted", false, nil},
		{"admin suspends", models.RoleAdmin, models.RoleUser, models.RoleSuspended, "abuse", false, nil},
		{"admin reinstates", models.RoleAdmin, models.RoleSuspended, models.RoleUser, "appeal granted", false, nil},
		{"admin cannot promote", models.RoleAdmin, models.RoleUser, models.RoleAdmin, "", false, ErrPermissionDenied},
		{"admin cannot demote admin", models.RoleAdmin, models.RoleAdmin, models.RoleUser, "", false, ErrPermissionDenied},
		{"user denied", models.RoleUser, models.RoleUser, models.RoleSuspended, "abuse", false, ErrPermissionDenied},
		{"self admin denied", models.RoleAdmin, models.RoleAdmin, models.RoleUser, "", true, ErrPermissionDenied},
		{"sole super self denied", models.RoleSuperAdmin, models.RoleSuperAdmin, models.RoleAdmin, "", true, ErrPermissionDenied},
		{"super target denied", models.RoleSuperAdmin, models.RoleSuperAdmin, models.RoleAdmin, "", false, ErrPermissionDenied},
		{"enter suspension needs reason", models.RoleAdmin, models.RoleUser, models.RoleSuspended, "  ", false, ErrInvalidRoleChange},
		{"leave suspension needs reason", models.RoleAdmin, models.RoleSuspended, models.RoleUser, "", false, ErrInvalidRoleChange},
		{"unchanged denied", models.RoleSuperAdmin, models.RoleUser, models.RoleUser, "", false, ErrRoleUnchanged},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			actorID, targetID := uuid.New(), uuid.New()
			if tc.self {
				targetID = actorID
			}
			f := &accountFixture{users: map[uuid.UUID]models.User{
				actorID:  {ID: actorID, Email: "actor@example.com", Role: tc.actor},
				targetID: {ID: targetID, Email: "target@example.com", Role: tc.target},
			}}
			if tc.self {
				f.users[actorID] = models.User{ID: actorID, Email: "actor@example.com", Role: tc.actor}
			}
			s := &RoleService{Users: f, WithTransaction: func(_ context.Context, fn func(RoleStore) error) error {
				return fn(RoleStore{Users: f, Admin: f})
			}}
			got, err := s.ChangeUserRole(context.Background(), actorID, targetID, tc.to, tc.reason)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("error = %v, want %v", err, tc.wantErr)
			}
			if tc.wantErr == nil {
				if got.Role != tc.to || len(f.changes) != 1 || f.changes[0].CreatedBy == nil || *f.changes[0].CreatedBy != actorID {
					t.Fatalf("change or audit missing: %+v, %+v", got, f.changes)
				}
			} else if len(f.changes) != 0 {
				t.Fatalf("denied action was audited: %+v", f.changes)
			}
		})
	}
}

func TestRoleChangeConflictDoesNotAudit(t *testing.T) {
	actorID, targetID := uuid.New(), uuid.New()
	f := &accountFixture{users: map[uuid.UUID]models.User{
		actorID:  {ID: actorID, Role: models.RoleSuperAdmin},
		targetID: {ID: targetID, Role: models.RoleUser},
	}, writeErr: store.ErrRoleConflict}
	s := &RoleService{Users: f, WithTransaction: func(_ context.Context, fn func(RoleStore) error) error {
		return fn(RoleStore{Users: f, Admin: f})
	}}
	if _, err := s.ChangeUserRole(context.Background(), actorID, targetID, models.RoleAdmin, ""); !errors.Is(err, ErrConcurrentRoleEdit) {
		t.Fatalf("concurrent change = %v", err)
	}
	if len(f.changes) != 0 || f.users[targetID].Role != models.RoleUser {
		t.Fatal("conflict mutated role or audit")
	}
}
