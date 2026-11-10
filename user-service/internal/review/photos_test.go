// Domain tests for the review photo lifecycle. The storage port and the
// repository are fakes: these tests pin validation, preparation semantics and
// attachment behavior without a database or object store.
package review

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"connectrpc.com/connect"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/images"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/database/reviewdb"
	"github.com/google/uuid"
)

// fakeStorage is a scripted PhotoStorage. Inspect returns the object info for
// a key or ErrObjectNotFound when the key is absent.
type fakeStorage struct {
	grants    map[string]images.UploadGrant
	objects   map[string]images.ObjectInfo
	downloads map[string]images.DownloadGrant

	uploadTTL       time.Duration
	createUploadErr error
	inspectErr      error
	downloadErr     error
	deleteErr       error

	deleted []string
}

func (f *fakeStorage) CreateUpload(_ context.Context, in images.UploadRequest) (images.UploadGrant, error) {
	if f.createUploadErr != nil {
		return images.UploadGrant{}, f.createUploadErr
	}
	ttl := f.uploadTTL
	if ttl == 0 {
		ttl = time.Minute
	}
	grant, ok := f.grants[in.Key]
	if !ok {
		grant = images.UploadGrant{
			URL:       "https://storage.test/" + in.Key,
			Method:    "PUT",
			ExpiresAt: time.Now().Add(ttl),
		}
	}
	return grant, nil
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
	if f.downloadErr != nil {
		return images.DownloadGrant{}, f.downloadErr
	}
	grant, ok := f.downloads[key]
	if !ok {
		grant = images.DownloadGrant{URL: "https://storage.test/get/" + key, ExpiresAt: time.Now().Add(time.Minute)}
	}
	return grant, nil
}

func (f *fakeStorage) Delete(_ context.Context, key string) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	f.deleted = append(f.deleted, key)
	return nil
}

// fakeRepository photo surface, added to the review-domain fake.
func (f *fakeRepository) InsertReviewPhoto(_ context.Context, input ReviewPhotoInput) error {
	if f.insertPhotoErr != nil {
		return f.insertPhotoErr
	}
	f.insertPhotoCalls++
	f.insertedPhoto = input
	if f.photosByKey == nil {
		f.photosByKey = map[uuid.UUID]ReviewPhoto{}
	}
	f.photosByKey[input.ID] = ReviewPhoto{
		ID:          input.ID,
		OwnerID:     uuid.NullUUID{UUID: input.OwnerID, Valid: true},
		ObjectKey:   input.ObjectKey,
		ContentType: input.ContentType,
		SizeBytes:   input.SizeBytes,
		State:       photoStateUploading,
	}
	return nil
}

func (f *fakeRepository) GetReviewPhoto(_ context.Context, ownerID, id uuid.UUID) (ReviewPhoto, error) {
	photo, ok := f.photosByKey[id]
	if !ok || !photo.OwnerID.Valid || photo.OwnerID.UUID != ownerID {
		return ReviewPhoto{}, errPhotoNotFound
	}
	return photo, nil
}

func (f *fakeRepository) ListReviewOrderStatuses(
	_ context.Context,
	reviewerID uuid.UUID,
	orderIDs []uuid.UUID,
) (ReviewOrderStatuses, error) {
	want := make(map[uuid.UUID]bool, len(orderIDs))
	for _, id := range orderIDs {
		want[id] = true
	}
	var out ReviewOrderStatuses
	for _, review := range f.reviewsForOrder {
		if review.ReviewerID == reviewerID && want[review.OrderID] {
			out.ReviewedOrderIDs = append(out.ReviewedOrderIDs, review.OrderID)
		}
	}
	return out, nil
}

func (f *fakeRepository) ReviewForOrder(_ context.Context, reviewerID, orderID uuid.UUID) (Review, error) {
	for _, review := range f.reviewsForOrder {
		if review.ReviewerID == reviewerID && review.OrderID == orderID {
			return review, nil
		}
	}
	return Review{}, errReviewNotFound
}

func (f *fakeRepository) AttachReviewPhoto(
	_ context.Context,
	_ *reviewdb.Queries,
	ownerID, reviewID uuid.UUID,
	ordinal int,
	photo ReviewPhoto,
) (int64, error) {
	if f.attachErr != nil {
		return 0, f.attachErr
	}
	if f.attachRowsAffected == 0 {
		return 0, nil
	}
	f.attached = append(f.attached, attachRecord{ownerID, reviewID, ordinal, photo.ID})
	return 1, nil
}

func (f *fakeRepository) ListReviewPhotosForReviews(
	_ context.Context,
	reviewIDs []uuid.UUID,
) ([]ReviewPhotoAttached, error) {
	return f.photosForReviews, f.listPhotosErr
}

func (f *fakeRepository) ClaimOrphanReviewPhotos(
	_ context.Context,
	_, _ time.Time,
) ([]ClaimedReviewPhoto, error) {
	return f.claimedOrphans, f.claimErr
}

func (f *fakeRepository) DeleteOrphanReviewPhoto(_ context.Context, id uuid.UUID) (int64, error) {
	f.deletedOrphans = append(f.deletedOrphans, id)
	return 1, f.deleteOrphanErr
}

// withPhotos returns the test service with storage wired in.
func (f *fakeRepository) withPhotos(storage PhotoStorage) *Service {
	return &Service{repo: f, withTransaction: func(_ context.Context, operation func(*reviewdb.Queries) error) error {
		return operation(reviewdb.New(nil))
	}, photos: storage}
}

func newTestServiceWithPhotos(repo Repository, storage PhotoStorage) *Service {
	return &Service{
		repo: repo,
		withTransaction: func(_ context.Context, operation func(*reviewdb.Queries) error) error {
			return operation(reviewdb.New(nil))
		},
		photos: storage,
	}
}

func TestCreateReviewPhotoUploadValidatesShape(t *testing.T) {
	fake := &fakeRepository{exists: map[uuid.UUID]bool{}}
	service := newTestServiceWithPhotos(fake, &fakeStorage{})

	cases := []struct {
		name        string
		contentType string
		sizeBytes   int64
	}{
		{"unsupported type", "image/gif", 100},
		{"empty type", "", 100},
		{"zero size", "image/jpeg", 0},
		{"negative size", "image/jpeg", -5},
		{"oversized", "image/jpeg", 10000001},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := service.CreateReviewPhotoUpload(context.Background(), uuid.New(), tc.contentType, tc.sizeBytes)
			assertExternal(t, err, http.StatusBadRequest)
		})
	}
}

func TestCreateReviewPhotoUploadStoresStagingRow(t *testing.T) {
	fake := &fakeRepository{exists: map[uuid.UUID]bool{}}
	storage := &fakeStorage{}
	service := newTestServiceWithPhotos(fake, storage)

	photoID, grant, err := service.CreateReviewPhotoUpload(context.Background(), uuid.New(), "image/jpeg", 200)
	if err != nil {
		t.Fatalf("create upload: %v", err)
	}
	if fake.insertedPhoto.ObjectKey != photoKeyPrefix+photoID.String() {
		t.Fatalf("object key must derive from the photo id, got %q", fake.insertedPhoto.ObjectKey)
	}
	if !fake.insertedPhoto.UploadExpiresAt.Equal(grant.ExpiresAt) {
		t.Fatalf("stored expiry = %v, want grant expiry %v", fake.insertedPhoto.UploadExpiresAt, grant.ExpiresAt)
	}
	if grant.URL == "" {
		t.Fatal("grant URL must be returned")
	}
	if fake.insertedPhoto.ID != photoID || fake.insertedPhoto.SizeBytes != 200 {
		t.Fatalf("staging row mismatch: %+v", fake.insertedPhoto)
	}
}

func TestCreateReviewPhotoUploadWithoutStorage(t *testing.T) {
	fake := &fakeRepository{exists: map[uuid.UUID]bool{}}
	service := newTestServiceWithPhotos(fake, nil)

	_, _, err := service.CreateReviewPhotoUpload(context.Background(), uuid.New(), "image/jpeg", 100)
	assertExternal(t, err, http.StatusInternalServerError)
}

func TestCreateReviewPhotoUploadSigningFailureLeavesNoOrphanRow(t *testing.T) {
	fake := &fakeRepository{exists: map[uuid.UUID]bool{}}
	storage := &fakeStorage{createUploadErr: errors.New("storage unavailable")}
	service := newTestServiceWithPhotos(fake, storage)

	if _, _, err := service.CreateReviewPhotoUpload(context.Background(), uuid.New(), "image/jpeg", 100); err == nil {
		t.Fatal("signing failure must surface an error")
	}
	// The grant is signed before the staging row is persisted, so a failed
	// sign must not leave a row for the orphan reaper to collect.
	if fake.insertPhotoCalls != 0 {
		t.Fatalf("staging row inserted despite signing failure: %d insert calls", fake.insertPhotoCalls)
	}
}

func TestPreparePhotosRejectsForeignAndStale(t *testing.T) {
	owner := uuid.New()
	staged := ReviewPhoto{
		ID: uuid.New(), OwnerID: uuid.NullUUID{UUID: owner, Valid: true}, ObjectKey: photoKeyPrefix + uuid.NewString(),
		ContentType: "image/jpeg", SizeBytes: 300, State: photoStateUploading,
	}

	cases := []struct {
		name   string
		caller uuid.UUID
		photos map[uuid.UUID]ReviewPhoto
		ids    []uuid.UUID
	}{
		{"unknown photo", owner, map[uuid.UUID]ReviewPhoto{}, []uuid.UUID{uuid.New()}},
		{
			"foreign owner",
			uuid.New(), // the staged photo belongs to `owner`, not this caller
			map[uuid.UUID]ReviewPhoto{staged.ID: staged},
			[]uuid.UUID{staged.ID},
		},
		{
			"already attached",
			owner,
			map[uuid.UUID]ReviewPhoto{staged.ID: func() ReviewPhoto {
				attached := staged
				attached.State = photoStateAttached
				return attached
			}()},
			[]uuid.UUID{staged.ID},
		},
		{
			"duplicate id",
			owner,
			map[uuid.UUID]ReviewPhoto{staged.ID: staged},
			[]uuid.UUID{staged.ID, staged.ID},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeRepository{exists: map[uuid.UUID]bool{}, photosByKey: tc.photos}
			storage := &fakeStorage{objects: map[string]images.ObjectInfo{
				staged.ObjectKey: {Exists: true, Size: staged.SizeBytes, ContentType: staged.ContentType},
			}}
			service := fake.withPhotos(storage)
			if _, err := service.preparePhotos(context.Background(), tc.caller, tc.ids); err == nil {
				t.Fatal("expected rejection")
			}
		})
	}
}

func TestPreparePhotosRejectsTooMany(t *testing.T) {
	fake := &fakeRepository{exists: map[uuid.UUID]bool{}}
	service := fake.withPhotos(&fakeStorage{})
	ids := make([]uuid.UUID, MaxReviewPhotos+1)
	for i := range ids {
		ids[i] = uuid.New()
	}
	if _, err := service.preparePhotos(context.Background(), uuid.New(), ids); err == nil {
		t.Fatal("expected rejection above the photo cap")
	}
}

func TestPreparePhotosVerifiesStoredObjectShape(t *testing.T) {
	owner := uuid.New()
	staged := ReviewPhoto{
		ID: uuid.New(), OwnerID: uuid.NullUUID{UUID: owner, Valid: true}, ObjectKey: photoKeyPrefix + uuid.NewString(),
		ContentType: "image/jpeg", SizeBytes: 300, State: photoStateUploading,
	}

	cases := []struct {
		name  string
		owner uuid.UUID
		info  map[string]images.ObjectInfo
	}{
		{"missing object", owner, nil},
		{"size drift", owner, map[string]images.ObjectInfo{
			staged.ObjectKey: {Exists: true, Size: 301, ContentType: staged.ContentType},
		}},
		{"type drift", owner, map[string]images.ObjectInfo{
			staged.ObjectKey: {Exists: true, Size: staged.SizeBytes, ContentType: "image/png"},
		}},
		{"zero-length object", owner, map[string]images.ObjectInfo{
			staged.ObjectKey: {Exists: true, Size: 0, ContentType: staged.ContentType},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeRepository{
				exists:      map[uuid.UUID]bool{},
				photosByKey: map[uuid.UUID]ReviewPhoto{staged.ID: staged},
			}
			storage := &fakeStorage{objects: tc.info}
			service := fake.withPhotos(storage)
			_, err := service.preparePhotos(context.Background(), tc.owner, []uuid.UUID{staged.ID})
			assertExternal(t, err, http.StatusPreconditionFailed)
		})
	}
}

func TestPreparePhotosClassifiesStorageFaults(t *testing.T) {
	owner := uuid.New()
	staged := ReviewPhoto{
		ID: uuid.New(), OwnerID: uuid.NullUUID{UUID: owner, Valid: true}, ObjectKey: photoKeyPrefix + uuid.NewString(),
		ContentType: "image/jpeg", SizeBytes: 300, State: photoStateUploading,
	}
	cases := []struct {
		name       string
		inspectErr error
		status     int
		reason     string
	}{
		{
			name:       "transient storage fault is 503",
			inspectErr: fmt.Errorf("%w: %w", images.ErrStorageUnavailable, errors.New("dial tcp: refused")),
			status:     http.StatusServiceUnavailable,
			reason:     reasonUploadTemporarilyUnavailable,
		},
		{
			name:       "misconfigured storage is 500",
			inspectErr: fmt.Errorf("%w: %w", images.ErrStorageMisconfigured, errors.New("AccessDenied")),
			status:     http.StatusInternalServerError,
			reason:     reasonUploadUnavailable,
		},
		{
			name:       "unclassified storage fault is 500",
			inspectErr: errors.New("something odd"),
			status:     http.StatusInternalServerError,
			reason:     reasonUploadUnavailable,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeRepository{
				exists:      map[uuid.UUID]bool{},
				photosByKey: map[uuid.UUID]ReviewPhoto{staged.ID: staged},
			}
			storage := &fakeStorage{inspectErr: tc.inspectErr}
			service := fake.withPhotos(storage)
			_, err := service.preparePhotos(context.Background(), owner, []uuid.UUID{staged.ID})
			assertExternal(t, err, tc.status)
			if got := reasonOf(t, toConnectError(context.Background(), err)); got != tc.reason {
				t.Fatalf("reason = %q, want %q", got, tc.reason)
			}
		})
	}
}

func TestSubmitReviewAttachesInListOrder(t *testing.T) {
	reviewer := uuid.New()
	reviewee := uuid.New()
	fake := &fakeRepository{
		exists:             map[uuid.UUID]bool{reviewer: true, reviewee: true},
		photosByKey:        map[uuid.UUID]ReviewPhoto{},
		attachRowsAffected: 1,
	}
	var staged []ReviewPhoto
	storage := &fakeStorage{}
	service := fake.withPhotos(storage)
	ctx := context.Background()

	for range MaxReviewPhotos {
		photoID, _, err := service.CreateReviewPhotoUpload(ctx, reviewer, "image/jpeg", 200)
		if err != nil {
			t.Fatalf("create upload: %v", err)
		}
		photo, err := fake.GetReviewPhoto(ctx, reviewer, photoID)
		if err != nil {
			t.Fatalf("staged photo: %v", err)
		}
		staged = append(staged, photo)
	}

	// All objects exist with the staged shape, so preparation passes.
	fakeStorageObjects := map[string]images.ObjectInfo{}
	for _, photo := range staged {
		fakeStorageObjects[photo.ObjectKey] = images.ObjectInfo{
			Exists: true, Size: photo.SizeBytes, ContentType: photo.ContentType,
		}
	}
	storage.objects = fakeStorageObjects

	ids := make([]uuid.UUID, len(staged))
	for i, photo := range staged {
		ids[i] = photo.ID
	}
	if _, err := service.SubmitReview(
		ctx,
		reviewer,
		reviewee,
		uuid.New(),
		ReviewSubmission{Rating: 5, Comment: "great"},
		ids,
	); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if len(fake.attached) != MaxReviewPhotos {
		t.Fatalf("want %d attachments, got %d", MaxReviewPhotos, len(fake.attached))
	}
	for ordinal, record := range fake.attached {
		if record.ordinal != ordinal {
			t.Fatalf("attachment %d has ordinal %d", ordinal, record.ordinal)
		}
		if record.reviewID != fake.insertedID {
			t.Fatalf("attachment %d points at review %s, want %s", ordinal, record.reviewID, fake.insertedID)
		}
	}
}

func TestSubmitReviewFailsWhenAttachmentLost(t *testing.T) {
	reviewer := uuid.New()
	reviewee := uuid.New()
	staged := ReviewPhoto{
		ID:          uuid.New(),
		OwnerID:     uuid.NullUUID{UUID: reviewer, Valid: true},
		ObjectKey:   photoKeyPrefix + uuid.NewString(),
		ContentType: "image/jpeg",
		SizeBytes:   300,
		State:       photoStateUploading,
	}
	fake := &fakeRepository{
		exists:             map[uuid.UUID]bool{reviewer: true, reviewee: true},
		photosByKey:        map[uuid.UUID]ReviewPhoto{staged.ID: staged},
		attachRowsAffected: 0, // the guarded update matched nothing
	}
	storage := &fakeStorage{objects: map[string]images.ObjectInfo{
		staged.ObjectKey: {Exists: true, Size: staged.SizeBytes, ContentType: staged.ContentType},
	}}
	service := fake.withPhotos(storage)

	_, err := service.SubmitReview(
		context.Background(), reviewer, reviewee, uuid.New(),
		ReviewSubmission{Rating: 4, Comment: "ok"}, []uuid.UUID{staged.ID},
	)
	assertExternal(t, err, http.StatusPreconditionFailed)
}

func TestSubmitReviewWithoutStorageAndWithoutPhotos(t *testing.T) {
	reviewer := uuid.New()
	reviewee := uuid.New()
	fake := &fakeRepository{exists: map[uuid.UUID]bool{reviewer: true, reviewee: true}}
	service := newTestService(fake) // photos nil

	if _, err := service.SubmitReview(
		context.Background(), reviewer, reviewee, uuid.New(),
		ReviewSubmission{Rating: 5, Comment: "fine"}, nil,
	); err != nil {
		t.Fatalf("submit without photos must succeed without storage: %v", err)
	}
}

func TestFillPhotosMintsDownloadGrants(t *testing.T) {
	reviewID := uuid.New()
	fake := &fakeRepository{
		exists: map[uuid.UUID]bool{},
		photosForReviews: []ReviewPhotoAttached{
			{
				ID:          uuid.New(),
				ReviewID:    reviewID,
				ObjectKey:   photoKeyPrefix + uuid.NewString(),
				ContentType: "image/png",
				Ordinal:     0,
			},
		},
	}
	storage := &fakeStorage{}
	service := fake.withPhotos(storage)

	reviews := []Review{{ID: reviewID}}
	filled, err := service.fillPhotos(context.Background(), reviews)
	if err != nil {
		t.Fatalf("fill photos: %v", err)
	}
	if len(filled[0].Photos) != 1 {
		t.Fatalf("want 1 photo view, got %d", len(filled[0].Photos))
	}
	if filled[0].Photos[0].URL == "" {
		t.Fatal("download URL must be minted")
	}
}

func TestFillPhotosWithoutStorageLeavesReviewsUntouched(t *testing.T) {
	reviewID := uuid.New()
	fake := &fakeRepository{exists: map[uuid.UUID]bool{}}
	service := newTestService(fake)

	reviews := []Review{{ID: reviewID}}
	filled, err := service.fillPhotos(context.Background(), reviews)
	if err != nil {
		t.Fatalf("fill photos: %v", err)
	}
	if len(filled[0].Photos) != 0 {
		t.Fatal("no photos expected without storage")
	}
}

func TestCleanupOnceDeletesClaimedOrphans(t *testing.T) {
	claimed := []ClaimedReviewPhoto{
		{ID: uuid.New(), ObjectKey: photoKeyPrefix + uuid.NewString()},
		{ID: uuid.New(), ObjectKey: photoKeyPrefix + uuid.NewString()},
	}
	fake := &fakeRepository{exists: map[uuid.UUID]bool{}, claimedOrphans: claimed}
	storage := &fakeStorage{}
	service := fake.withPhotos(storage)

	if err := service.CleanupOnce(context.Background()); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if len(storage.deleted) != len(claimed) {
		t.Fatalf("want %d object deletions, got %d", len(claimed), len(storage.deleted))
	}
	if len(fake.deletedOrphans) != len(claimed) {
		t.Fatalf("want %d row deletions, got %d", len(claimed), len(fake.deletedOrphans))
	}
}

func TestCleanupOnceKeepsRowWhenObjectDeleteFails(t *testing.T) {
	claimed := []ClaimedReviewPhoto{{ID: uuid.New(), ObjectKey: photoKeyPrefix + uuid.NewString()}}
	fake := &fakeRepository{exists: map[uuid.UUID]bool{}, claimedOrphans: claimed}
	storage := &fakeStorage{deleteErr: errors.New("storage unavailable")}
	service := fake.withPhotos(storage)

	if err := service.CleanupOnce(context.Background()); err == nil {
		t.Fatal("expected the object deletion failure to propagate")
	}
	if len(fake.deletedOrphans) != 0 {
		t.Fatal("the row must survive until the object is deleted")
	}
}

func TestCleanupOnceWithoutStorage(t *testing.T) {
	fake := &fakeRepository{exists: map[uuid.UUID]bool{}}
	service := newTestService(fake)
	if err := service.CleanupOnce(context.Background()); err != nil {
		t.Fatalf("cleanup without storage must be a no-op: %v", err)
	}
}

// Compile-time guard: the fake implements the storage port.
var _ PhotoStorage = (*fakeStorage)(nil)

func TestCreateReviewPhotoUploadClassifiesSigningFailures(t *testing.T) {
	for _, tc := range []struct {
		name   string
		cause  error
		code   connect.Code
		reason string
	}{
		{"transient", fmt.Errorf("credentials: %w", images.ErrStorageUnavailable), connect.CodeUnavailable, reasonUploadTemporarilyUnavailable},
		{"misconfigured", images.ErrStorageMisconfigured, connect.CodeInternal, reasonUploadUnavailable},
		{"unknown", errors.New("provider detail"), connect.CodeInternal, reasonUploadUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &fakeRepository{}
			service := newTestServiceWithPhotos(repo, &fakeStorage{createUploadErr: tc.cause})
			_, _, err := service.CreateReviewPhotoUpload(context.Background(), uuid.New(), "image/jpeg", 100)
			rpcErr := toConnectError(context.Background(), err)
			if connect.CodeOf(rpcErr) != tc.code || reasonOf(t, rpcErr) != tc.reason {
				t.Fatalf("signing error = %v, want %v/%s", rpcErr, tc.code, tc.reason)
			}
			if repo.insertPhotoCalls != 0 {
				t.Fatal("failed signing must not persist a staging row")
			}
		})
	}
}
