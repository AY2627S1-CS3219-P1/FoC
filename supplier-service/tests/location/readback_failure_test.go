package location_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	additionrequest "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/location/additionrequest"
	disablement "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/location/disablement"
	"github.com/google/uuid"
)

// Fail only the read after a successful write, using the existing fake's
// transaction rollback. Initial reads and other fake behavior remain unchanged.
type readbackFailureRepository struct {
	repository *TestRepository
	failure    error
	writes     int
}

func (r *readbackFailureRepository) Within(ctx context.Context, run func(testTx) error) error {
	return r.repository.Within(ctx, func(tx testTx) error { return run(&readbackFailureTx{testTx: tx, repository: r}) })
}

type readbackFailureTx struct {
	testTx
	repository *readbackFailureRepository
}

func (t *readbackFailureTx) SaveDisablement(ctx context.Context, d disablement.Disablement, revision int64) error {
	err := t.testTx.SaveDisablement(ctx, d, revision)
	if err == nil {
		t.repository.writes++
	}
	return err
}
func (t *readbackFailureTx) SaveRequest(ctx context.Context, r additionrequest.AdditionRequest, revision int64) error {
	err := t.testTx.SaveRequest(ctx, r, revision)
	if err == nil {
		t.repository.writes++
	}
	return err
}
func (t *readbackFailureTx) Disablement(ctx context.Context, id string) (disablement.Disablement, error) {
	if t.repository.writes > 0 {
		return disablement.Disablement{}, t.repository.failure
	}
	return t.testTx.Disablement(ctx, id)
}
func (t *readbackFailureTx) Request(ctx context.Context, id string) (additionrequest.AdditionRequest, error) {
	if t.repository.writes > 0 {
		return additionrequest.AdditionRequest{}, t.repository.failure
	}
	return t.testTx.Request(ctx, id)
}

func TestMutationReadbackFailureRollsBack(t *testing.T) {
	now := time.Date(2026, 9, 28, 13, 0, 0, 417, time.UTC)
	ctx := context.Background()
	for _, operation := range []string{"update disablement", "end", "cancel", "update request", "withdraw", "reject", "approve"} {
		t.Run(operation, func(t *testing.T) {
			repository := newTestRepository()
			repository.locations[locationID] = additionrequest.Location{ID: locationID}
			dID, rID := uuid.NewString(), uuid.NewString()
			start := now.Add(time.Hour)
			if operation == "end" {
				start = now.Add(-time.Hour)
			}
			repository.disablements[dID] = disablement.Disablement{ID: dID, LocationID: locationID, StartsAt: start, Reason: "original", CreatedBy: admin.ID, Revision: 1, CreatedAt: now.Add(-2 * time.Hour), UpdatedAt: now.Add(-2 * time.Hour)}
			repository.requests[rID] = additionrequest.AdditionRequest{ID: rID, Proposal: additionrequest.Proposal{Name: "original", BuildingID: buildingID, Latitude: 1.294, Longitude: 103.774}, SubmittedBy: owner.ID, Status: additionrequest.Pending, Revision: 1, CreatedAt: now.Add(-2 * time.Hour), UpdatedAt: now.Add(-2 * time.Hour)}
			locations, disablements, requests := copyValue(repository.locations), copyValue(repository.disablements), copyValue(repository.requests)
			failure := additionrequest.DependencyError("read saved resource", context.DeadlineExceeded)
			decorated := &readbackFailureRepository{repository: repository, failure: failure}
			service := newTestService(decorated, func() time.Time { return now })
			var err error
			switch operation {
			case "update disablement":
				_, err = service.UpdateDisablement(ctx, admin, disablement.UpdateDisablement{ID: dID, ExpectedRevision: 1, Paths: []string{"reason"}, Reason: "changed"})
			case "end":
				_, err = service.EndDisablement(ctx, admin, dID)
			case "cancel":
				_, err = service.CancelDisablement(ctx, admin, dID)
			case "update request":
				_, err = service.UpdateRequest(ctx, owner, additionrequest.UpdateRequest{ID: rID, ExpectedRevision: 1, Paths: []string{"details"}, Proposal: additionrequest.Proposal{Details: "changed"}})
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
