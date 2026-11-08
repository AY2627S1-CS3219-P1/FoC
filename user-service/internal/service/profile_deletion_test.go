package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/models"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/store"
	"github.com/google/uuid"
)

type fakeDeletionJobs struct {
	job        store.DeletionJob
	err        error
	calls      int
	calledWith uuid.UUID
}

func (f *fakeDeletionJobs) GetMyDeletionJob(_ context.Context, userID uuid.UUID) (store.DeletionJob, error) {
	f.calls++
	f.calledWith = userID
	return f.job, f.err
}

func TestGetMyAccountDeletionStatusLiveAccountIsClean(t *testing.T) {
	userID := uuid.New()
	users := &accountFixture{users: map[uuid.UUID]models.User{
		userID: {ID: userID, Email: "person@example.com", Role: models.RoleUser},
	}}
	jobs := &fakeDeletionJobs{}
	service := &ProfileService{Users: users, DeletionJobs: jobs}

	status, err := service.GetMyAccountDeletionStatus(context.Background(), userID)
	if err != nil {
		t.Fatal(err)
	}
	if status.IsDeleting || status.RequestedAt != nil || status.LastCheckedAt != nil {
		t.Fatalf("live account reported deletion state: %+v", status)
	}
	if jobs.calls != 0 {
		t.Fatal("live account queried a deletion job")
	}
}

func TestGetMyAccountDeletionStatusReturnsOwnPendingData(t *testing.T) {
	userID := uuid.New()
	requested := time.Date(2026, 11, 8, 8, 0, 0, 0, time.UTC)
	checked := requested.Add(time.Hour)
	users := &accountFixture{users: map[uuid.UUID]models.User{
		userID: {
			ID: userID, Email: "person@example.com", Role: models.RoleUser,
			IsDeleting: true, DeletionRequestedAt: &requested,
		},
	}}
	jobs := &fakeDeletionJobs{job: store.DeletionJob{
		HasJob: true, LastCheckedAt: checked, WaitingReasons: []string{"order_outstanding", "credit_reservation"},
	}}
	service := &ProfileService{Users: users, DeletionJobs: jobs}

	status, err := service.GetMyAccountDeletionStatus(context.Background(), userID)
	if err != nil {
		t.Fatal(err)
	}
	if !status.IsDeleting || status.RequestedAt == nil || !status.RequestedAt.Equal(requested) {
		t.Fatalf("deleting state = %+v", status)
	}
	if status.LastCheckedAt == nil || !status.LastCheckedAt.Equal(checked) {
		t.Fatalf("last checked = %v", status.LastCheckedAt)
	}
	if len(status.WaitingReasons) != 2 || status.WaitingReasons[0] != "order_outstanding" {
		t.Fatalf("waiting reasons = %v", status.WaitingReasons)
	}
	if jobs.calls != 1 || jobs.calledWith != userID {
		t.Fatalf("job lookup used another account: calls=%d user=%v", jobs.calls, jobs.calledWith)
	}
}

func TestGetMyAccountDeletionStatusRequiresLiveCaller(t *testing.T) {
	users := &accountFixture{users: map[uuid.UUID]models.User{}}
	service := &ProfileService{Users: users}

	if _, err := service.GetMyAccountDeletionStatus(
		context.Background(),
		uuid.Nil,
	); !errors.Is(
		err,
		ErrUnauthenticated,
	) {
		t.Fatalf("nil actor: %v", err)
	}
	_, err := service.GetMyAccountDeletionStatus(context.Background(), uuid.New())
	if !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("missing user: %v", err)
	}
}

func TestGetMyAccountDeletionStatusPropagatesJobFailure(t *testing.T) {
	userID := uuid.New()
	requested := time.Date(2026, 11, 8, 8, 0, 0, 0, time.UTC)
	users := &accountFixture{users: map[uuid.UUID]models.User{
		userID: {ID: userID, Role: models.RoleUser, IsDeleting: true, DeletionRequestedAt: &requested},
	}}
	jobs := &fakeDeletionJobs{err: errors.New("job store unavailable")}
	service := &ProfileService{Users: users, DeletionJobs: jobs}

	if _, err := service.GetMyAccountDeletionStatus(context.Background(), userID); err == nil {
		t.Fatal("job failure was swallowed")
	}
}

// TestUpdateMyProfileRejectsDeletingAccount proves the server is authoritative:
// a deleting account cannot edit its profile even though the frontend hides the
// form. The request is rejected before any write.
func TestUpdateMyProfileRejectsDeletingAccount(t *testing.T) {
	userID := uuid.New()
	users := &accountFixture{users: map[uuid.UUID]models.User{
		userID: {ID: userID, Email: "person@example.com", DisplayName: "P", Role: models.RoleUser,
			IsDeleting: true},
	}}
	service := &ProfileService{Users: users}

	_, err := service.UpdateMyProfile(context.Background(), userID, ProfileInput{
		DisplayName: "New Name",
		PhoneNumber: "+6591234567",
	})
	if !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("deleting account profile edit error = %v, want ErrPermissionDenied", err)
	}
}
