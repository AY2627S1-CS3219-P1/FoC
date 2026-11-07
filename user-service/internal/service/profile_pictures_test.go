package service

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/images"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/models"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/store"
	"github.com/google/uuid"
)

type fakeProfileUsers struct {
	user      *models.User
	getErr    error
	updateErr error
	updated   *models.User
}

func (f *fakeProfileUsers) GetByID(context.Context, uuid.UUID) (*models.User, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.user, nil
}

func (f *fakeProfileUsers) UpdateActiveProfile(_ context.Context, user *models.User) error {
	if f.updateErr != nil {
		return f.updateErr
	}
	f.updated = user
	f.user = user
	return nil
}

type fakeProfilePictureStore struct {
	inserted    *models.ProfilePictureUpload
	insertErr   error
	upload      *models.ProfilePictureUpload
	getErr      error
	oldKey      *string
	setErr      error
	clearOldKey *string
	clearErr    error
	claimed     []models.ProfilePictureUpload
	claimErr    error
	deletedIDs  []uuid.UUID
	deleteErr   error
	onSet       func()
}

func (f *fakeProfilePictureStore) InsertUpload(_ context.Context, upload *models.ProfilePictureUpload) error {
	f.inserted = upload
	return f.insertErr
}

func (f *fakeProfilePictureStore) GetUpload(
	context.Context,
	uuid.UUID,
	uuid.UUID,
) (*models.ProfilePictureUpload, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.upload, nil
}

func (f *fakeProfilePictureStore) SetUserPicture(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (*string, error) {
	if f.onSet != nil {
		f.onSet()
	}
	return f.oldKey, f.setErr
}

func (f *fakeProfilePictureStore) ClearUserPicture(context.Context, uuid.UUID, uuid.UUID) (*string, error) {
	return f.clearOldKey, f.clearErr
}

func (f *fakeProfilePictureStore) ClaimExpiredUploads(
	context.Context,
	time.Time,
	int,
) ([]models.ProfilePictureUpload, error) {
	return f.claimed, f.claimErr
}

func (f *fakeProfilePictureStore) DeleteUploads(_ context.Context, ids []uuid.UUID) error {
	f.deletedIDs = ids
	return f.deleteErr
}

type fakeProfilePictureStorage struct {
	grants    map[string]images.UploadGrant
	objects   map[string]images.ObjectInfo
	downloads map[string]images.DownloadGrant

	createErr   error
	inspectErr  error
	downloadErr error
	deleteErr   error
	deleted     []string
}

func (f *fakeProfilePictureStorage) CreateUpload(
	_ context.Context,
	in images.UploadRequest,
) (images.UploadGrant, error) {
	if f.createErr != nil {
		return images.UploadGrant{}, f.createErr
	}
	grant, ok := f.grants[in.Key]
	if !ok {
		grant = images.UploadGrant{
			URL:       "https://storage.test/" + in.Key,
			Method:    "PUT",
			ExpiresAt: time.Now().Add(time.Minute),
		}
	}
	return grant, nil
}

func (f *fakeProfilePictureStorage) Inspect(_ context.Context, key string) (images.ObjectInfo, error) {
	if f.inspectErr != nil {
		return images.ObjectInfo{}, f.inspectErr
	}
	info, ok := f.objects[key]
	if !ok {
		return images.ObjectInfo{}, images.ErrObjectNotFound
	}
	return info, nil
}

func (f *fakeProfilePictureStorage) CreateDownload(
	_ context.Context,
	key string,
) (images.DownloadGrant, error) {
	if f.downloadErr != nil {
		return images.DownloadGrant{}, f.downloadErr
	}
	grant, ok := f.downloads[key]
	if !ok {
		grant = images.DownloadGrant{URL: "https://storage.test/get/" + key, ExpiresAt: time.Now().Add(time.Minute)}
	}
	return grant, nil
}

func (f *fakeProfilePictureStorage) Delete(_ context.Context, key string) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	f.deleted = append(f.deleted, key)
	return nil
}

func profilePictureTestUser(role models.RoleName) *models.User {
	return &models.User{ID: uuid.New(), DisplayName: "Person", Role: role}
}

func profilePictureTestService(
	user *models.User,
	photos *fakeProfilePictureStore,
	storage *fakeProfilePictureStorage,
) *ProfileService {
	return &ProfileService{
		Users:    &fakeProfileUsers{user: user},
		Pictures: photos,
		Storage:  storage,
		Now:      func() time.Time { return time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC) },
	}
}

func assertExternalStatus(t *testing.T, err error, want int) {
	t.Helper()
	var external api.ExternalError
	if !errors.As(err, &external) || external.Code() != want {
		t.Fatalf("want external error with status %d, got: %v", want, err)
	}
}

func TestCreateProfilePictureUpload(t *testing.T) {
	user := profilePictureTestUser(models.RoleUser)
	photos := &fakeProfilePictureStore{}
	storage := &fakeProfilePictureStorage{}
	svc := profilePictureTestService(user, photos, storage)

	upload, grant, err := svc.CreateProfilePictureUpload(context.Background(), user.ID, "image/png", 1234)
	if err != nil {
		t.Fatal(err)
	}
	if upload.ID == uuid.Nil || upload.ObjectKey != profilePictureKey(user.ID, upload.ID) {
		t.Fatalf("unexpected upload: %+v", upload)
	}
	if photos.inserted == nil || photos.inserted.UserID != user.ID || photos.inserted.SizeBytes != 1234 {
		t.Fatalf("pending upload not stored: %+v", photos.inserted)
	}
	if grant.URL != "https://storage.test/"+upload.ObjectKey {
		t.Fatalf("grant = %+v", grant)
	}

	for _, tc := range []struct {
		name        string
		contentType string
		size        int64
	}{
		{"bad type", "text/plain", 1},
		{"empty", "image/png", 0},
		{"too large", "image/png", MaxProfilePictureBytes + 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := svc.CreateProfilePictureUpload(context.Background(), user.ID, tc.contentType, tc.size)
			assertExternalStatus(t, err, http.StatusBadRequest)
		})
	}
}

func TestCreateProfilePictureUploadUnavailableAndSuspended(t *testing.T) {
	user := profilePictureTestUser(models.RoleUser)
	svc := &ProfileService{Users: &fakeProfileUsers{user: user}}
	_, _, err := svc.CreateProfilePictureUpload(context.Background(), user.ID, "image/png", 1)
	assertExternalStatus(t, err, http.StatusPreconditionFailed)

	user.Role = models.RoleSuspended
	svc = profilePictureTestService(user, &fakeProfilePictureStore{}, &fakeProfilePictureStorage{})
	_, _, err = svc.CreateProfilePictureUpload(context.Background(), user.ID, "image/png", 1)
	assertExternalStatus(t, err, http.StatusForbidden)
}

func TestUpdateMyProfilePicture(t *testing.T) {
	user := profilePictureTestUser(models.RoleUser)
	oldKey := "profile/picture/old"
	user.ProfilePictureKey = &oldKey
	uploadID := uuid.New()
	key := profilePictureKey(user.ID, uploadID)
	photos := &fakeProfilePictureStore{
		upload: &models.ProfilePictureUpload{ID: uploadID, UserID: user.ID, ObjectKey: key,
			ContentType: "image/png", SizeBytes: 4},
		oldKey: &oldKey,
	}
	storage := &fakeProfilePictureStorage{
		objects: map[string]images.ObjectInfo{key: {Exists: true, Size: 4, ContentType: "image/png"}},
	}
	svc := profilePictureTestService(user, photos, storage)
	// The store consumes the row and the next read must expose the new key.
	photos.onSet = func() { user.ProfilePictureKey = &key }

	view, err := svc.UpdateMyProfilePicture(context.Background(), user.ID, uploadID)
	if err != nil {
		t.Fatal(err)
	}
	if view.PictureURL == "" || view.ProfilePictureKey == nil || *view.ProfilePictureKey != key {
		t.Fatalf("profile view = %+v", view)
	}
	if len(storage.deleted) != 1 || storage.deleted[0] != oldKey {
		t.Fatalf("old picture not deleted: %v", storage.deleted)
	}
}

func TestUpdateMyProfilePictureRejectsInvalidUploads(t *testing.T) {
	user := profilePictureTestUser(models.RoleUser)
	uploadID := uuid.New()
	key := profilePictureKey(user.ID, uploadID)
	base := func() (*fakeProfilePictureStore, *fakeProfilePictureStorage) {
		return &fakeProfilePictureStore{
			upload: &models.ProfilePictureUpload{ID: uploadID, UserID: user.ID, ObjectKey: key,
				ContentType: "image/png", SizeBytes: 4},
		}, &fakeProfilePictureStorage{objects: map[string]images.ObjectInfo{
			key: {Exists: true, Size: 4, ContentType: "image/png"},
		}}
	}

	t.Run("foreign upload", func(t *testing.T) {
		photos, storage := base()
		photos.getErr = store.ErrUploadNotFound
		_, err := profilePictureTestService(user, photos, storage).
			UpdateMyProfilePicture(context.Background(), user.ID, uploadID)
		assertExternalStatus(t, err, http.StatusBadRequest)
	})
	t.Run("missing object", func(t *testing.T) {
		photos, storage := base()
		storage.objects = map[string]images.ObjectInfo{}
		_, err := profilePictureTestService(user, photos, storage).
			UpdateMyProfilePicture(context.Background(), user.ID, uploadID)
		assertExternalStatus(t, err, http.StatusBadRequest)
	})
	t.Run("metadata mismatch", func(t *testing.T) {
		photos, storage := base()
		storage.objects[key] = images.ObjectInfo{Exists: true, Size: 5, ContentType: "image/png"}
		_, err := profilePictureTestService(user, photos, storage).
			UpdateMyProfilePicture(context.Background(), user.ID, uploadID)
		assertExternalStatus(t, err, http.StatusBadRequest)
	})
	t.Run("suspended", func(t *testing.T) {
		photos, storage := base()
		user.Role = models.RoleSuspended
		defer func() { user.Role = models.RoleUser }()
		_, err := profilePictureTestService(user, photos, storage).
			UpdateMyProfilePicture(context.Background(), user.ID, uploadID)
		assertExternalStatus(t, err, http.StatusForbidden)
	})
}

func TestUpdateMyProfilePictureIsIdempotent(t *testing.T) {
	t.Run("already consumed before the read", func(t *testing.T) {
		user := profilePictureTestUser(models.RoleUser)
		uploadID := uuid.New()
		key := profilePictureKey(user.ID, uploadID)
		user.ProfilePictureKey = &key
		svc := profilePictureTestService(
			user, &fakeProfilePictureStore{getErr: store.ErrUploadNotFound}, &fakeProfilePictureStorage{},
		)

		view, err := svc.UpdateMyProfilePicture(context.Background(), user.ID, uploadID)
		if err != nil || view.PictureURL == "" {
			t.Fatalf("idempotent retry = %+v, %v", view, err)
		}
	})

	t.Run("consumed between the read and the update", func(t *testing.T) {
		user := profilePictureTestUser(models.RoleUser)
		uploadID := uuid.New()
		key := profilePictureKey(user.ID, uploadID)
		user.ProfilePictureKey = &key
		photos := &fakeProfilePictureStore{
			upload: &models.ProfilePictureUpload{ID: uploadID, UserID: user.ID, ObjectKey: key,
				ContentType: "image/png", SizeBytes: 4},
			setErr: store.ErrUploadNotFound,
		}
		storage := &fakeProfilePictureStorage{objects: map[string]images.ObjectInfo{
			key: {Exists: true, Size: 4, ContentType: "image/png"},
		}}
		svc := profilePictureTestService(user, photos, storage)

		view, err := svc.UpdateMyProfilePicture(context.Background(), user.ID, uploadID)
		if err != nil || view.PictureURL == "" {
			t.Fatalf("idempotent retry after lost row = %+v, %v", view, err)
		}
	})
}

func TestRemoveMyProfilePicture(t *testing.T) {
	user := profilePictureTestUser(models.RoleUser)
	oldKey := "profile/picture/old"
	user.ProfilePictureKey = &oldKey
	photos := &fakeProfilePictureStore{clearOldKey: &oldKey}
	storage := &fakeProfilePictureStorage{}
	svc := profilePictureTestService(user, photos, storage)

	if _, err := svc.RemoveMyProfilePicture(context.Background(), user.ID); err != nil {
		t.Fatal(err)
	}
	if len(storage.deleted) != 1 || storage.deleted[0] != oldKey {
		t.Fatalf("old picture not deleted: %v", storage.deleted)
	}

	photos.clearErr = store.ErrRoleConflict
	_, err := svc.RemoveMyProfilePicture(context.Background(), user.ID)
	assertExternalStatus(t, err, http.StatusForbidden)
}

func TestGetMyProfileSignsPictureURL(t *testing.T) {
	user := profilePictureTestUser(models.RoleUser)
	key := "profile/picture/current"
	user.ProfilePictureKey = &key
	svc := profilePictureTestService(user, &fakeProfilePictureStore{}, &fakeProfilePictureStorage{})
	view, err := svc.GetMyProfile(context.Background(), user.ID)
	if err != nil || view.PictureURL == "" {
		t.Fatalf("signed profile = %+v, %v", view, err)
	}

	svc.Storage = &fakeProfilePictureStorage{downloadErr: errors.New("down")}
	view, err = svc.GetMyProfile(context.Background(), user.ID)
	if err != nil || view.PictureURL != "" {
		t.Fatalf("degraded profile = %+v, %v", view, err)
	}
}

func TestCleanupOnce(t *testing.T) {
	key := "profile/picture/orphan"
	photos := &fakeProfilePictureStore{claimed: []models.ProfilePictureUpload{{ID: uuid.New(), ObjectKey: key}}}
	storage := &fakeProfilePictureStorage{}
	svc := &ProfileService{Pictures: photos, Storage: storage}

	if err := svc.CleanupOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(storage.deleted) != 1 || storage.deleted[0] != key || len(photos.deletedIDs) != 1 {
		t.Fatalf("cleanup: deleted=%v rows=%v", storage.deleted, photos.deletedIDs)
	}

	photos = &fakeProfilePictureStore{
		claimed: []models.ProfilePictureUpload{{ID: uuid.New(), ObjectKey: key}},
	}
	storage = &fakeProfilePictureStorage{deleteErr: errors.New("down")}
	svc = &ProfileService{Pictures: photos, Storage: storage}
	if err := svc.CleanupOnce(context.Background()); err == nil || len(photos.deletedIDs) != 0 {
		t.Fatalf("failed cleanup should keep rows: %v %v", err, photos.deletedIDs)
	}
}
