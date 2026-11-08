package service

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
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/models"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/store"
	"github.com/google/uuid"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
)

type fakeProfileUsers struct {
	user      *models.User
	queue     []*models.User
	getErr    error
	updateErr error
	updated   *models.User
}

func (f *fakeProfileUsers) GetByID(context.Context, uuid.UUID) (*models.User, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	if len(f.queue) > 0 {
		user := f.queue[0]
		f.queue = f.queue[1:]
		return user, nil
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
	inserted      *models.ProfilePicture
	insertErr     error
	pending       *models.ProfilePicture
	getPendingErr error
	current       *models.ProfilePicture
	getByIDErr    error
	oldKey        *string
	activateErr   error
	onActivate    func()
	deactivateKey *string
	deactivateErr error
	claimed       []models.ProfilePicture
	claimErr      error
	claimBefore   time.Time
	claimLimit    int
	deleting      []models.ProfilePicture
	listErr       error
	deletedIDs    []uuid.UUID
	deleteErr     error
}

func (f *fakeProfilePictureStore) InsertPending(_ context.Context, picture *models.ProfilePicture) error {
	f.inserted = picture
	return f.insertErr
}

func (f *fakeProfilePictureStore) GetPending(
	context.Context,
	uuid.UUID,
	uuid.UUID,
) (*models.ProfilePicture, error) {
	if f.getPendingErr != nil {
		return nil, f.getPendingErr
	}
	return f.pending, nil
}

func (f *fakeProfilePictureStore) GetPictureByID(context.Context, uuid.UUID) (*models.ProfilePicture, error) {
	if f.getByIDErr != nil {
		return nil, f.getByIDErr
	}
	return f.current, nil
}

func (f *fakeProfilePictureStore) Activate(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (*string, error) {
	if f.onActivate != nil {
		f.onActivate()
	}
	return f.oldKey, f.activateErr
}

func (f *fakeProfilePictureStore) Deactivate(context.Context, uuid.UUID, uuid.UUID) (*string, error) {
	return f.deactivateKey, f.deactivateErr
}

func (f *fakeProfilePictureStore) ClaimExpiredPending(
	_ context.Context,
	before time.Time,
	limit int,
) ([]models.ProfilePicture, error) {
	f.claimBefore = before
	f.claimLimit = limit
	return f.claimed, f.claimErr
}

func (f *fakeProfilePictureStore) ListDeleting(context.Context, int) ([]models.ProfilePicture, error) {
	return f.deleting, f.listErr
}

func (f *fakeProfilePictureStore) DeletePicture(_ context.Context, id uuid.UUID) error {
	f.deletedIDs = append(f.deletedIDs, id)
	return f.deleteErr
}

type fakeProfilePictureStorage struct {
	grants        map[string]images.UploadGrant
	objects       map[string]images.ObjectInfo
	downloads     map[string]images.DownloadGrant
	createErr     error
	inspectErr    error
	downloadErr   error
	deleteErr     error
	deleteErrKeys map[string]error
	deleted       []string
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
	if err := f.deleteErrKeys[key]; err != nil {
		return err
	}
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
	pictures *fakeProfilePictureStore,
	storage *fakeProfilePictureStorage,
) *ProfileService {
	return &ProfileService{
		Users:    &fakeProfileUsers{user: user},
		Pictures: pictures,
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

func ptrID(id uuid.UUID) *uuid.UUID { return &id }

func TestProfileConnectErrorDetails(t *testing.T) {
	connectErr := ProfileConnectError(context.Background(), invalidProfilePicture("bad picture"))
	if connect.CodeOf(connectErr) != connect.CodeInvalidArgument {
		t.Fatalf("code = %v", connect.CodeOf(connectErr))
	}
	var ce *connect.Error
	if !errors.As(connectErr, &ce) {
		t.Fatalf("not a connect error: %T", connectErr)
	}
	var reason string
	var detail *commonv1.ErrorDetail
	for _, d := range ce.Details() {
		msg, err := d.Value()
		if err != nil {
			t.Fatal(err)
		}
		switch value := msg.(type) {
		case *errdetails.ErrorInfo:
			reason = value.GetReason()
		case *commonv1.ErrorDetail:
			detail = value
		}
	}
	if reason != reasonInvalidProfilePicture || detail == nil ||
		detail.GetReason() != reasonInvalidProfilePicture || detail.GetMessage() != "bad picture" {
		t.Fatalf("details: reason=%q detail=%+v", reason, detail)
	}
}

func TestCreateProfilePictureUpload(t *testing.T) {
	user := profilePictureTestUser(models.RoleUser)
	pictures := &fakeProfilePictureStore{}
	storage := &fakeProfilePictureStorage{}
	svc := profilePictureTestService(user, pictures, storage)

	upload, grant, err := svc.CreateProfilePictureUpload(context.Background(), user.ID, "image/png", 1234)
	if err != nil {
		t.Fatal(err)
	}
	if upload.ID == uuid.Nil {
		t.Fatalf("unexpected upload: %+v", upload)
	}
	if pictures.inserted == nil || pictures.inserted.UserID != user.ID ||
		pictures.inserted.SizeBytes != 1234 || pictures.inserted.State != models.ProfilePicturePending {
		t.Fatalf("pending row not stored: %+v", pictures.inserted)
	}
	if grant.URL != "https://storage.test/"+pictures.inserted.ObjectKey {
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
	assertExternalStatus(t, err, http.StatusInternalServerError)

	user.Role = models.RoleSuspended
	svc = profilePictureTestService(user, &fakeProfilePictureStore{}, &fakeProfilePictureStorage{})
	_, _, err = svc.CreateProfilePictureUpload(context.Background(), user.ID, "image/png", 1)
	assertExternalStatus(t, err, http.StatusForbidden)
}

func TestCreateProfilePictureUploadErrors(t *testing.T) {
	user := profilePictureTestUser(models.RoleUser)
	pictures := &fakeProfilePictureStore{insertErr: errors.New("db")}
	storage := &fakeProfilePictureStorage{}
	svc := profilePictureTestService(user, pictures, storage)
	if _, _, err := svc.CreateProfilePictureUpload(
		context.Background(), user.ID, "image/png", 1,
	); err == nil {
		t.Fatal("expected insert failure")
	}

	pictures = &fakeProfilePictureStore{}
	storage = &fakeProfilePictureStorage{createErr: images.ErrStorageUnavailable}
	svc = profilePictureTestService(user, pictures, storage)
	_, _, err := svc.CreateProfilePictureUpload(context.Background(), user.ID, "image/png", 1)
	assertExternalStatus(t, err, http.StatusServiceUnavailable)

	storage = &fakeProfilePictureStorage{createErr: errors.New("storage")}
	svc = profilePictureTestService(user, pictures, storage)
	_, _, err = svc.CreateProfilePictureUpload(context.Background(), user.ID, "image/png", 1)
	assertExternalStatus(t, err, http.StatusInternalServerError)
}

func TestCleanupOnceErrors(t *testing.T) {
	storage := &fakeProfilePictureStorage{}
	pictures := &fakeProfilePictureStore{claimErr: errors.New("claim")}
	svc := &ProfileService{Pictures: pictures, Storage: storage}
	if err := svc.CleanupOnce(context.Background()); err == nil {
		t.Fatal("expected claim failure")
	}

	pictures = &fakeProfilePictureStore{listErr: errors.New("list")}
	svc = &ProfileService{Pictures: pictures, Storage: storage}
	if err := svc.CleanupOnce(context.Background()); err == nil {
		t.Fatal("expected list failure")
	}
}

func TestGetMyProfileReadErrorDegrades(t *testing.T) {
	user := profilePictureTestUser(models.RoleUser)
	pictureID := uuid.New()
	user.ProfilePictureID = ptrID(pictureID)
	pictures := &fakeProfilePictureStore{getByIDErr: errors.New("db")}
	svc := profilePictureTestService(user, pictures, &fakeProfilePictureStorage{})
	view, err := svc.GetMyProfile(context.Background(), user.ID)
	if err != nil || view.PictureURL != "" {
		t.Fatalf("read-error degradation = %+v, %v", view, err)
	}
}

func TestRemoveMyProfilePictureWithoutStorage(t *testing.T) {
	user := profilePictureTestUser(models.RoleUser)
	svc := &ProfileService{Users: &fakeProfileUsers{user: user}, Pictures: &fakeProfilePictureStore{}}
	_, err := svc.RemoveMyProfilePicture(context.Background(), user.ID)
	assertExternalStatus(t, err, http.StatusInternalServerError)
}

func TestUpdateMyProfilePicture(t *testing.T) {
	user := profilePictureTestUser(models.RoleUser)
	oldID := uuid.New()
	oldKey := "profile/picture/old"
	user.ProfilePictureID = ptrID(oldID)
	pictureID := uuid.New()
	key := profilePictureKey(user.ID, pictureID)
	pending := &models.ProfilePicture{ID: pictureID, UserID: user.ID, ObjectKey: key,
		ContentType: "image/png", SizeBytes: 4, State: models.ProfilePicturePending}
	pictures := &fakeProfilePictureStore{pending: pending, oldKey: &oldKey}
	storage := &fakeProfilePictureStorage{
		objects: map[string]images.ObjectInfo{key: {Exists: true, Size: 4, ContentType: "image/png"}},
	}
	pictures.onActivate = func() {
		user.ProfilePictureID = ptrID(pictureID)
		pictures.current = &models.ProfilePicture{ID: pictureID, UserID: user.ID, ObjectKey: key,
			ContentType: "image/png", SizeBytes: 4, State: models.ProfilePictureActive}
	}
	svc := profilePictureTestService(user, pictures, storage)

	view, err := svc.UpdateMyProfilePicture(context.Background(), user.ID, pictureID)
	if err != nil {
		t.Fatal(err)
	}
	if view.PictureURL == "" || view.ProfilePictureID == nil || *view.ProfilePictureID != pictureID {
		t.Fatalf("profile view = %+v", view)
	}
	if len(storage.deleted) != 1 || storage.deleted[0] != oldKey {
		t.Fatalf("old picture not deleted: %v", storage.deleted)
	}
}

func TestUpdateMyProfilePictureRejectsInvalidUploads(t *testing.T) {
	user := profilePictureTestUser(models.RoleUser)
	pictureID := uuid.New()
	key := profilePictureKey(user.ID, pictureID)
	base := func() (*fakeProfilePictureStore, *fakeProfilePictureStorage) {
		pictures := &fakeProfilePictureStore{
			pending: &models.ProfilePicture{ID: pictureID, UserID: user.ID, ObjectKey: key,
				ContentType: "image/png", SizeBytes: 4, State: models.ProfilePicturePending},
		}
		storage := &fakeProfilePictureStorage{objects: map[string]images.ObjectInfo{
			key: {Exists: true, Size: 4, ContentType: "image/png"},
		}}
		return pictures, storage
	}

	t.Run("foreign upload", func(t *testing.T) {
		pictures, storage := base()
		pictures.getPendingErr = store.ErrPictureNotFound
		_, err := profilePictureTestService(user, pictures, storage).
			UpdateMyProfilePicture(context.Background(), user.ID, pictureID)
		assertExternalStatus(t, err, http.StatusPreconditionFailed)
	})
	t.Run("missing object", func(t *testing.T) {
		pictures, storage := base()
		storage.objects = map[string]images.ObjectInfo{}
		_, err := profilePictureTestService(user, pictures, storage).
			UpdateMyProfilePicture(context.Background(), user.ID, pictureID)
		assertExternalStatus(t, err, http.StatusPreconditionFailed)
	})
	t.Run("metadata mismatch", func(t *testing.T) {
		pictures, storage := base()
		storage.objects[key] = images.ObjectInfo{Exists: true, Size: 5, ContentType: "image/png"}
		_, err := profilePictureTestService(user, pictures, storage).
			UpdateMyProfilePicture(context.Background(), user.ID, pictureID)
		assertExternalStatus(t, err, http.StatusPreconditionFailed)
	})
	t.Run("suspended", func(t *testing.T) {
		pictures, storage := base()
		user.Role = models.RoleSuspended
		defer func() { user.Role = models.RoleUser }()
		_, err := profilePictureTestService(user, pictures, storage).
			UpdateMyProfilePicture(context.Background(), user.ID, pictureID)
		assertExternalStatus(t, err, http.StatusForbidden)
	})
}

func TestUpdateMyProfilePictureIsIdempotent(t *testing.T) {
	t.Run("already active before the read", func(t *testing.T) {
		user := profilePictureTestUser(models.RoleUser)
		pictureID := uuid.New()
		key := profilePictureKey(user.ID, pictureID)
		user.ProfilePictureID = ptrID(pictureID)
		pictures := &fakeProfilePictureStore{
			getPendingErr: store.ErrPictureNotFound,
			current: &models.ProfilePicture{ID: pictureID, UserID: user.ID, ObjectKey: key,
				ContentType: "image/png", SizeBytes: 4, State: models.ProfilePictureActive},
		}
		view, err := profilePictureTestService(user, pictures, &fakeProfilePictureStorage{}).
			UpdateMyProfilePicture(context.Background(), user.ID, pictureID)
		if err != nil || view.PictureURL == "" {
			t.Fatalf("idempotent retry = %+v, %v", view, err)
		}
	})

	t.Run("committed after the first user read", func(t *testing.T) {
		pictureID := uuid.New()
		userID := uuid.New()
		key := profilePictureKey(userID, pictureID)
		stale := &models.User{ID: userID, DisplayName: "Person", Role: models.RoleUser}
		fresh := &models.User{ID: userID, DisplayName: "Person", Role: models.RoleUser,
			ProfilePictureID: ptrID(pictureID)}
		pictures := &fakeProfilePictureStore{
			getPendingErr: store.ErrPictureNotFound,
			current: &models.ProfilePicture{ID: pictureID, UserID: userID, ObjectKey: key,
				ContentType: "image/png", SizeBytes: 4, State: models.ProfilePictureActive},
		}
		svc := &ProfileService{
			Users:    &fakeProfileUsers{queue: []*models.User{stale, fresh}, user: fresh},
			Pictures: pictures,
			Storage:  &fakeProfilePictureStorage{},
		}
		view, err := svc.UpdateMyProfilePicture(context.Background(), userID, pictureID)
		if err != nil || view.PictureURL == "" {
			t.Fatalf("idempotent retry after stale read = %+v, %v", view, err)
		}
	})

	t.Run("activated between the read and the update", func(t *testing.T) {
		user := profilePictureTestUser(models.RoleUser)
		pictureID := uuid.New()
		key := profilePictureKey(user.ID, pictureID)
		pending := &models.ProfilePicture{ID: pictureID, UserID: user.ID, ObjectKey: key,
			ContentType: "image/png", SizeBytes: 4, State: models.ProfilePicturePending}
		pictures := &fakeProfilePictureStore{
			pending:     pending,
			activateErr: store.ErrPictureNotFound,
			current: &models.ProfilePicture{ID: pictureID, UserID: user.ID, ObjectKey: key,
				ContentType: "image/png", SizeBytes: 4, State: models.ProfilePictureActive},
		}
		storage := &fakeProfilePictureStorage{objects: map[string]images.ObjectInfo{
			key: {Exists: true, Size: 4, ContentType: "image/png"},
		}}
		user.ProfilePictureID = ptrID(pictureID)
		view, err := profilePictureTestService(user, pictures, storage).
			UpdateMyProfilePicture(context.Background(), user.ID, pictureID)
		if err != nil || view.PictureURL == "" {
			t.Fatalf("idempotent retry after lost activation = %+v, %v", view, err)
		}
	})
}

func TestRemoveMyProfilePicture(t *testing.T) {
	user := profilePictureTestUser(models.RoleUser)
	oldKey := "profile/picture/old"
	pictures := &fakeProfilePictureStore{deactivateKey: &oldKey}
	storage := &fakeProfilePictureStorage{}
	svc := profilePictureTestService(user, pictures, storage)

	if _, err := svc.RemoveMyProfilePicture(context.Background(), user.ID); err != nil {
		t.Fatal(err)
	}
	if len(storage.deleted) != 1 || storage.deleted[0] != oldKey {
		t.Fatalf("old picture not deleted: %v", storage.deleted)
	}

	pictures.deactivateErr = store.ErrRoleConflict
	_, err := svc.RemoveMyProfilePicture(context.Background(), user.ID)
	assertExternalStatus(t, err, http.StatusForbidden)
}

func TestGetMyProfileSignsPictureURL(t *testing.T) {
	user := profilePictureTestUser(models.RoleUser)
	pictureID := uuid.New()
	key := "profile/picture/current"
	user.ProfilePictureID = ptrID(pictureID)
	pictures := &fakeProfilePictureStore{current: &models.ProfilePicture{ID: pictureID, UserID: user.ID,
		ObjectKey: key, ContentType: "image/png", SizeBytes: 4, State: models.ProfilePictureActive}}
	svc := profilePictureTestService(user, pictures, &fakeProfilePictureStorage{})
	view, err := svc.GetMyProfile(context.Background(), user.ID)
	if err != nil || view.PictureURL == "" {
		t.Fatalf("signed profile = %+v, %v", view, err)
	}

	pictures.current.State = models.ProfilePictureDeleting
	view, err = svc.GetMyProfile(context.Background(), user.ID)
	if err != nil || view.PictureURL != "" {
		t.Fatalf("non-active picture = %+v, %v", view, err)
	}

	pictures.current.State = models.ProfilePictureActive
	svc.Storage = &fakeProfilePictureStorage{downloadErr: errors.New("down")}
	view, err = svc.GetMyProfile(context.Background(), user.ID)
	if err != nil || view.PictureURL != "" {
		t.Fatalf("degraded profile = %+v, %v", view, err)
	}
}

func TestCleanupOnce(t *testing.T) {
	key := "profile/picture/orphan"
	pictureID := uuid.New()
	pictures := &fakeProfilePictureStore{
		claimed:  []models.ProfilePicture{{ID: pictureID, ObjectKey: key}},
		deleting: []models.ProfilePicture{{ID: pictureID, ObjectKey: key}},
	}
	storage := &fakeProfilePictureStorage{}
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	svc := &ProfileService{Pictures: pictures, Storage: storage,
		Now: func() time.Time { return now }}

	if err := svc.CleanupOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !pictures.claimBefore.Equal(now.Add(-24*time.Hour)) ||
		pictures.claimLimit != profilePictureCleanupBatch {
		t.Fatalf("cleanup window = %v, batch = %d", pictures.claimBefore, pictures.claimLimit)
	}
	if len(storage.deleted) != 1 || storage.deleted[0] != key ||
		len(pictures.deletedIDs) != 1 || pictures.deletedIDs[0] != pictureID {
		t.Fatalf("cleanup: deleted=%v rows=%v", storage.deleted, pictures.deletedIDs)
	}

	pictures = &fakeProfilePictureStore{
		deleting: []models.ProfilePicture{{ID: pictureID, ObjectKey: key}},
	}
	storage = &fakeProfilePictureStorage{deleteErr: errors.New("down")}
	svc = &ProfileService{Pictures: pictures, Storage: storage}
	if err := svc.CleanupOnce(context.Background()); err == nil || len(pictures.deletedIDs) != 0 {
		t.Fatalf("failed cleanup should keep rows: %v %v", err, pictures.deletedIDs)
	}
}

func TestCleanupOnceContinuesPastFailedObject(t *testing.T) {
	firstID, secondID := uuid.New(), uuid.New()
	firstKey, secondKey := "profile/picture/first", "profile/picture/second"
	pictures := &fakeProfilePictureStore{deleting: []models.ProfilePicture{
		{ID: firstID, ObjectKey: firstKey},
		{ID: secondID, ObjectKey: secondKey},
	}}
	storage := &fakeProfilePictureStorage{deleteErrKeys: map[string]error{
		firstKey: errors.New("down"),
	}}
	svc := &ProfileService{Pictures: pictures, Storage: storage}

	if err := svc.CleanupOnce(context.Background()); err == nil {
		t.Fatal("expected the failed object to be reported")
	}
	if len(pictures.deletedIDs) != 1 || pictures.deletedIDs[0] != secondID {
		t.Fatalf("later rows should still be deleted: %v", pictures.deletedIDs)
	}
	if len(storage.deleted) != 1 || storage.deleted[0] != secondKey {
		t.Fatalf("deleted objects = %v", storage.deleted)
	}
}
