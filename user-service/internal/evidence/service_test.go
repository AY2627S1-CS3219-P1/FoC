package evidence

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api"
	commonv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/common/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/images"
	"github.com/google/uuid"
)

type fakeRepository struct {
	staged   map[uuid.UUID]Staged
	owners   map[uuid.UUID]uuid.UUID
	attached []Attached
	located  map[uuid.UUID]Located
	orphans  []Orphan
	deleted  []uuid.UUID
}

func newFakeRepository() *fakeRepository {
	return &fakeRepository{
		staged:  map[uuid.UUID]Staged{},
		owners:  map[uuid.UUID]uuid.UUID{},
		located: map[uuid.UUID]Located{},
	}
}

func (f *fakeRepository) InsertStaged(_ context.Context, input StagedInput) error {
	f.staged[input.ID] = Staged{
		Prepared: Prepared{
			ID:          input.ID,
			ObjectKey:   input.ObjectKey,
			ContentType: input.ContentType,
			SizeBytes:   input.SizeBytes,
		},
		State: stateUploading,
	}
	f.owners[input.ID] = input.OwnerID
	return nil
}

func (f *fakeRepository) GetStaged(_ context.Context, ownerID, id uuid.UUID) (Staged, error) {
	staged, ok := f.staged[id]
	if !ok || f.owners[id] != ownerID {
		return Staged{}, errEvidenceNotFound
	}
	return staged, nil
}

func (f *fakeRepository) ListForParents(context.Context, []Parent) ([]Attached, error) {
	return f.attached, nil
}

func (f *fakeRepository) Locate(_ context.Context, id uuid.UUID) (Located, error) {
	located, ok := f.located[id]
	if !ok {
		return Located{}, errEvidenceNotFound
	}
	return located, nil
}

func (f *fakeRepository) ClaimOrphans(context.Context, time.Time, time.Time) ([]Orphan, error) {
	return f.orphans, nil
}

func (f *fakeRepository) DeleteOrphan(_ context.Context, id uuid.UUID) error {
	f.deleted = append(f.deleted, id)
	return nil
}

// fakeStorage serves objects from memory.
type fakeStorage struct {
	objects    map[string]images.ObjectInfo
	inspectErr error
	signErr    error
	deleted    []string
}

func (f *fakeStorage) CreateUpload(_ context.Context, request images.UploadRequest) (images.UploadGrant, error) {
	if f.signErr != nil {
		return images.UploadGrant{}, f.signErr
	}
	return images.UploadGrant{URL: "https://storage/" + request.Key, Method: http.MethodPut}, nil
}

func (f *fakeStorage) Inspect(_ context.Context, key string) (images.ObjectInfo, error) {
	if f.inspectErr != nil {
		return images.ObjectInfo{}, f.inspectErr
	}
	info, ok := f.objects[key]
	if !ok {
		return images.ObjectInfo{}, images.ErrObjectNotFound
	}
	return info, nil
}

func (f *fakeStorage) CreateDownload(_ context.Context, key string) (images.DownloadGrant, error) {
	return images.DownloadGrant{URL: "https://storage/" + key + "?signed"}, nil
}

func (f *fakeStorage) Delete(_ context.Context, key string) error {
	f.deleted = append(f.deleted, key)
	return nil
}

func wantReason(t *testing.T, err error, reason string, status int) {
	t.Helper()

	var evidenceErr *Error
	if !errors.As(err, &evidenceErr) || evidenceErr.Reason() != reason {
		t.Fatalf("error = %v, want reason %s", err, reason)
	}
	var external api.ExternalError
	if !errors.As(err, &external) || external.Code() != status {
		t.Fatalf("error = %v, want HTTP status %d", err, status)
	}
}

func TestCreateUpload(t *testing.T) {
	ctx := context.Background()
	owner := uuid.New()

	t.Run("signs, then stages the image", func(t *testing.T) {
		repo, storage := newFakeRepository(), &fakeStorage{}
		id, grant, err := NewService(repo, storage).CreateUpload(ctx, owner, "image/png", 100)
		if err != nil {
			t.Fatal(err)
		}
		if grant.URL != "https://storage/evidence/"+id.String() || repo.owners[id] != owner {
			t.Fatalf("grant = %+v, staged owner = %v", grant, repo.owners[id])
		}
	})

	t.Run("rejects invalid metadata", func(t *testing.T) {
		service := NewService(newFakeRepository(), &fakeStorage{})
		_, _, err := service.CreateUpload(ctx, owner, "image/gif", 100)
		wantReason(t, err, ReasonInvalidEvidence, http.StatusBadRequest)
		_, _, err = service.CreateUpload(ctx, owner, "image/png", maxImageBytes+1)
		wantReason(t, err, ReasonInvalidEvidence, http.StatusBadRequest)
	})

	t.Run("a signing failure stages nothing", func(t *testing.T) {
		repo := newFakeRepository()
		storage := &fakeStorage{signErr: images.ErrStorageUnavailable}
		_, _, err := NewService(repo, storage).CreateUpload(ctx, owner, "image/png", 100)
		wantReason(t, err, ReasonUploadTemporarilyUnavailable, http.StatusServiceUnavailable)
		if len(repo.staged) != 0 {
			t.Fatal("a failed grant left a staged row")
		}
	})

	t.Run("without storage uploads are unavailable", func(t *testing.T) {
		_, _, err := NewService(newFakeRepository(), nil).CreateUpload(ctx, owner, "image/png", 100)
		wantReason(t, err, ReasonUploadUnavailable, http.StatusInternalServerError)
	})
}

func TestPrepare(t *testing.T) {
	ctx := context.Background()
	owner := uuid.New()

	stage := func(repo *fakeRepository, storage *fakeStorage, uploaded bool) uuid.UUID {
		id, _, err := NewService(repo, storage).CreateUpload(ctx, owner, "image/jpeg", 50)
		if err != nil {
			t.Fatal(err)
		}
		if uploaded {
			storage.objects["evidence/"+id.String()] = images.ObjectInfo{
				Exists: true, Size: 50, ContentType: "image/jpeg",
			}
		}
		return id
	}

	t.Run("no images needs no storage", func(t *testing.T) {
		prepared, err := NewService(newFakeRepository(), nil).Prepare(ctx, owner, nil)
		if err != nil || prepared != nil {
			t.Fatalf("prepared = %v, err = %v", prepared, err)
		}
	})

	t.Run("prepares uploaded images in order", func(t *testing.T) {
		repo, storage := newFakeRepository(), &fakeStorage{objects: map[string]images.ObjectInfo{}}
		first, second := stage(repo, storage, true), stage(repo, storage, true)
		prepared, err := NewService(repo, storage).Prepare(ctx, owner, []uuid.UUID{second, first})
		if err != nil {
			t.Fatal(err)
		}
		if len(prepared) != 2 || prepared[0].ID != second || prepared[1].ID != first {
			t.Fatalf("prepared = %+v", prepared)
		}
	})

	t.Run("rejects images that are not ready", func(t *testing.T) {
		repo, storage := newFakeRepository(), &fakeStorage{objects: map[string]images.ObjectInfo{}}
		service := NewService(repo, storage)
		missing := stage(repo, storage, false)
		_, err := service.Prepare(ctx, owner, []uuid.UUID{missing})
		wantReason(t, err, ReasonEvidenceNotReady, http.StatusPreconditionFailed)

		resized := stage(repo, storage, true)
		storage.objects["evidence/"+resized.String()] = images.ObjectInfo{
			Exists: true, Size: 51, ContentType: "image/jpeg",
		}
		_, err = service.Prepare(ctx, owner, []uuid.UUID{resized})
		wantReason(t, err, ReasonEvidenceNotReady, http.StatusPreconditionFailed)

		_, err = service.Prepare(ctx, uuid.New(), []uuid.UUID{missing})
		wantReason(t, err, ReasonEvidenceNotStaged, http.StatusBadRequest)
	})

	t.Run("rejects duplicate and excess images", func(t *testing.T) {
		repo, storage := newFakeRepository(), &fakeStorage{objects: map[string]images.ObjectInfo{}}
		service := NewService(repo, storage)
		id := stage(repo, storage, true)
		_, err := service.Prepare(ctx, owner, []uuid.UUID{id, id})
		wantReason(t, err, ReasonInvalidEvidence, http.StatusBadRequest)
		_, err = service.Prepare(ctx, owner, make([]uuid.UUID, MaxPerParent+1))
		wantReason(t, err, ReasonInvalidEvidence, http.StatusBadRequest)
	})

	t.Run("storage outages are reported", func(t *testing.T) {
		repo, storage := newFakeRepository(), &fakeStorage{objects: map[string]images.ObjectInfo{}}
		id := stage(repo, storage, true)
		storage.inspectErr = images.ErrStorageUnavailable
		_, err := NewService(repo, storage).Prepare(ctx, owner, []uuid.UUID{id})
		wantReason(t, err, ReasonUploadTemporarilyUnavailable, http.StatusServiceUnavailable)

		_, err = NewService(repo, nil).Prepare(ctx, owner, []uuid.UUID{id})
		wantReason(t, err, ReasonUploadUnavailable, http.StatusInternalServerError)
	})
}

func TestViewsAndDownload(t *testing.T) {
	ctx := context.Background()
	repo, storage := newFakeRepository(), &fakeStorage{}
	report, response := ReportParent(uuid.New()), ReportResponseParent(uuid.New())
	first, second, third := uuid.New(), uuid.New(), uuid.New()
	repo.attached = []Attached{
		{ID: first, ObjectKey: "evidence/a", ContentType: "image/png", Parent: report},
		{ID: second, ObjectKey: "evidence/b", ContentType: "image/png", Parent: report},
		{ID: third, ObjectKey: "evidence/c", ContentType: "image/webp", Parent: response},
	}
	service := NewService(repo, storage)

	views, err := service.Views(ctx, []Parent{report, response})
	if err != nil {
		t.Fatal(err)
	}
	if len(views[report]) != 2 || views[report][1].ID != second ||
		views[response][0].URL != "https://storage/evidence/c?signed" {
		t.Fatalf("views = %+v", views)
	}
	if views, err := NewService(repo, nil).Views(ctx, []Parent{report}); err != nil || len(views) != 0 {
		t.Fatalf("views without storage = %+v, %v", views, err)
	}

	repo.located[first] = Located{Owner: Owner{ReportID: report.ID}, ObjectKey: "evidence/a", ContentType: "image/png"}
	if owner, err := service.Locate(ctx, first); err != nil || owner.ReportID != report.ID {
		t.Fatalf("locate = %+v, %v", owner, err)
	}
	if view, err := service.Download(ctx, first); err != nil || view.URL != "https://storage/evidence/a?signed" {
		t.Fatalf("download = %+v, %v", view, err)
	}
	if _, err := service.Locate(ctx, uuid.New()); err == nil {
		t.Fatal("expected an unknown image to be not found")
	}
}

func TestCleanupOnce(t *testing.T) {
	repo, storage := newFakeRepository(), &fakeStorage{}
	orphan := Orphan{ID: uuid.New(), ObjectKey: "evidence/orphan"}
	repo.orphans = []Orphan{orphan}

	if err := NewService(repo, storage).CleanupOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(storage.deleted) != 1 || storage.deleted[0] != orphan.ObjectKey || len(repo.deleted) != 1 {
		t.Fatalf("deleted objects = %v, rows = %v", storage.deleted, repo.deleted)
	}
}

func TestToConnectErrorNamesTheImage(t *testing.T) {
	id := uuid.New()
	err := ToConnectError(context.Background(), notReady(id, "image upload is missing; upload it again"))

	var connectErr *connect.Error
	if !errors.As(err, &connectErr) || connectErr.Code() != connect.CodeFailedPrecondition {
		t.Fatalf("error = %v", err)
	}
	for _, detail := range connectErr.Details() {
		value, valueErr := detail.Value()
		if valueErr != nil {
			continue
		}
		if errorDetail, ok := value.(*commonv1.ErrorDetail); ok {
			if errorDetail.GetReason() != ReasonEvidenceNotReady ||
				len(errorDetail.GetFieldViolations()) != 1 ||
				errorDetail.GetFieldViolations()[0].GetDescription() != id.String()+": image upload is missing; upload it again" {
				t.Fatalf("detail = %+v", errorDetail)
			}
			return
		}
	}
	t.Fatal("error has no ErrorDetail")
}
