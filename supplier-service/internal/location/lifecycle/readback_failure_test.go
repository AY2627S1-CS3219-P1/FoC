package lifecycle_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	w "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/location/lifecycle"
	"github.com/google/uuid"
)

// Fail only the read after a successful write, using the existing fake's
// transaction rollback. Initial reads and other fake behavior remain unchanged.
type readbackFailureRepository struct {
	repository *TestRepository
	failure    error
	writes     int
}

func (r *readbackFailureRepository) Within(ctx context.Context, run func(w.Tx) error) error {
	return r.repository.Within(ctx, func(tx w.Tx) error { return run(&readbackFailureTx{Tx: tx, repository: r}) })
}

type readbackFailureTx struct {
	w.Tx
	repository *readbackFailureRepository
}

func (t *readbackFailureTx) SaveDisablement(ctx context.Context, d w.Disablement, revision int64) error {
	err := t.Tx.SaveDisablement(ctx, d, revision)
	if err == nil {
		t.repository.writes++
	}
	return err
}
func (t *readbackFailureTx) SaveRequest(ctx context.Context, r w.AdditionRequest, revision int64) error {
	err := t.Tx.SaveRequest(ctx, r, revision)
	if err == nil {
		t.repository.writes++
	}
	return err
}
func (t *readbackFailureTx) Disablement(ctx context.Context, id string) (w.Disablement, error) {
	if t.repository.writes > 0 {
		return w.Disablement{}, t.repository.failure
	}
	return t.Tx.Disablement(ctx, id)
}
func (t *readbackFailureTx) Request(ctx context.Context, id string) (w.AdditionRequest, error) {
	if t.repository.writes > 0 {
		return w.AdditionRequest{}, t.repository.failure
	}
	return t.Tx.Request(ctx, id)
}

func TestMutationReadbackFailureRollsBack(t *testing.T) {
	now := time.Date(2026, 9, 28, 13, 0, 0, 417, time.UTC)
	ctx := context.Background()
	for _, operation := range []string{"update disablement", "end", "cancel", "update request", "withdraw", "reject", "approve"} {
		t.Run(operation, func(t *testing.T) {
			repository := newTestRepository()
			repository.locations[locationID] = w.Location{ID: locationID}
			dID, rID := uuid.NewString(), uuid.NewString()
			start := now.Add(time.Hour)
			if operation == "end" {
				start = now.Add(-time.Hour)
			}
			repository.disablements[dID] = w.Disablement{ID: dID, LocationID: locationID, StartsAt: start, Reason: "original", CreatedBy: admin.ID, Revision: 1, CreatedAt: now.Add(-2 * time.Hour), UpdatedAt: now.Add(-2 * time.Hour)}
			repository.requests[rID] = w.AdditionRequest{ID: rID, Proposal: w.Proposal{Name: "original", BuildingID: buildingID, Latitude: 1.294, Longitude: 103.774}, SubmittedBy: owner.ID, Status: w.Pending, Revision: 1, CreatedAt: now.Add(-2 * time.Hour), UpdatedAt: now.Add(-2 * time.Hour)}
			locations, disablements, requests := copyValue(repository.locations), copyValue(repository.disablements), copyValue(repository.requests)
			failure := w.DependencyError("read saved resource", context.DeadlineExceeded)
			decorated := &readbackFailureRepository{repository: repository, failure: failure}
			service := w.NewService(decorated, func() time.Time { return now })
			var err error
			switch operation {
			case "update disablement":
				_, err = service.UpdateDisablement(ctx, admin, w.UpdateDisablement{ID: dID, ExpectedRevision: 1, Paths: []string{"reason"}, Reason: "changed"})
			case "end":
				_, err = service.EndDisablement(ctx, admin, dID)
			case "cancel":
				_, err = service.CancelDisablement(ctx, admin, dID)
			case "update request":
				_, err = service.UpdateRequest(ctx, owner, w.UpdateRequest{ID: rID, ExpectedRevision: 1, Paths: []string{"details"}, Proposal: w.Proposal{Details: "changed"}})
			case "withdraw":
				_, err = service.WithdrawRequest(ctx, owner, rID)
			case "reject":
				_, err = service.RejectRequest(ctx, admin, rID, "not suitable")
			case "approve":
				_, err = service.ApproveRequest(ctx, admin, rID)
			}
			if err != failure {
				t.Fatalf("readback error identity changed: got=%v want=%v", err, failure)
			}
			if decorated.writes != 1 {
				t.Fatalf("failure did not follow successful Save: writes=%d", decorated.writes)
			}
			if !reflect.DeepEqual(repository.locations, locations) || !reflect.DeepEqual(repository.disablements, disablements) || !reflect.DeepEqual(repository.requests, requests) {
				t.Fatal("readback failure did not roll back resource/revision/audit/relationship writes")
			}
		})
	}
}
