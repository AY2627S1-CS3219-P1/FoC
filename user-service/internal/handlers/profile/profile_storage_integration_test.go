package profile_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"os"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth"
	userv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/images"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/database"
	profilehandler "github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/handlers/profile"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/models"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/service"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/store"
	"github.com/google/uuid"
)

// This opt-in test requires a throwaway PostgreSQL database and S3-compatible
// endpoint with AWS credentials. It uses authenticated caller contexts at the
// handler boundary; router/JWT authorization has separate integration coverage.
func TestProfilePictureStorageLifecycle(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	endpoint := os.Getenv("TEST_PROFILE_STORAGE_ENDPOINT")
	if dsn == "" || endpoint == "" {
		t.Skip("TEST_DATABASE_URL and TEST_PROFILE_STORAGE_ENDPOINT must be set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	db, err := database.Open(dsn, 5, 5)
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	bucket := os.Getenv("TEST_PROFILE_STORAGE_BUCKET")
	if bucket == "" {
		bucket = "profile-storage-integration"
	}
	storage, err := images.NewS3(ctx, images.S3Config{
		Bucket:         bucket,
		Endpoint:       endpoint,
		PublicEndpoint: endpoint,
		UploadTTL:      time.Minute,
		DownloadTTL:    time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := storage.InitializeLocalBucket(ctx, "http://localhost:5173"); err != nil {
		t.Fatal(err)
	}
	persistence := store.New(db)
	logic := &service.ProfileService{
		Users:    persistence.Users,
		Pictures: persistence.ProfilePictures,
		Storage:  storage,
	}
	handler := &profilehandler.Handler{Logic: logic}
	newCaller := func() (models.User, context.Context) {
		t.Helper()
		user := models.User{
			Email:       uuid.NewString() + "@example.com",
			DisplayName: "Storage test",
			Role:        models.RoleUser,
		}
		if err := persistence.Users.Create(ctx, &user); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			// Delete objects before removing their durable references, even on failure.
			var pictures []models.ProfilePicture
			if err := db.Where("user_id = ?", user.ID).Find(&pictures).Error; err != nil {
				t.Error(err)
			}
			for _, picture := range pictures {
				if err := storage.Delete(context.Background(), picture.ObjectKey); err != nil {
					t.Error(err)
				}
			}
			if err := db.Unscoped().Delete(&models.User{}, user.ID).Error; err != nil {
				t.Error(err)
			}
		})
		return user, auth.WithCaller(ctx, auth.Caller{ID: user.ID.String()})
	}
	user, caller := newCaller()
	_, other := newCaller()
	// A real one-pixel PNG, rather than metadata-only dummy bytes.
	png, err := base64.StdEncoding.DecodeString(
		"iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jRZkAAAAASUVORK5CYII=",
	)
	if err != nil {
		t.Fatal(err)
	}
	httpClient := &http.Client{
		Timeout:       10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	upload := func() string {
		t.Helper()
		grant, err := handler.CreateProfilePictureUpload(
			caller,
			connect.NewRequest(&userv1.CreateProfilePictureUploadRequest{
				ContentType: "image/png",
				SizeBytes:   int64(len(png)),
			}),
		)
		if err != nil {
			t.Fatal(err)
		}
		if grant.Header().Get("Cache-Control") != "no-store" || grant.Msg.GetPictureId() == "" ||
			grant.Msg.GetExpiresAt() == nil || !grant.Msg.GetExpiresAt().AsTime().After(time.Now()) {
			t.Fatalf("invalid upload grant: %+v", grant.Msg)
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodPut, grant.Msg.GetUploadUrl(), bytes.NewReader(png))
		if err != nil {
			t.Fatal(err)
		}
		for name, value := range grant.Msg.GetUploadHeaders() {
			request.Header.Set(name, value)
		}
		// No cookie jar or Authorization header: only the signed grant is sent.
		response, err := httpClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = response.Body.Close() }()
		if response.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(response.Body)
			t.Fatalf("signed PUT returned %d: %s", response.StatusCode, body)
		}
		return grant.Msg.GetPictureId()
	}
	apply := func(id string) *userv1.Profile {
		t.Helper()
		response, err := handler.UpdateMyProfilePicture(
			caller,
			connect.NewRequest(&userv1.UpdateMyProfilePictureRequest{PictureId: id}),
		)
		if err != nil {
			t.Fatal(err)
		}
		if response.Header().Get("Cache-Control") != "no-store" ||
			response.Msg.GetProfile().GetProfilePictureId() != id {
			t.Fatalf("invalid apply response: %+v", response.Msg)
		}
		return response.Msg.GetProfile()
	}
	checkDownload := func(profile *userv1.Profile) {
		t.Helper()
		if profile.GetProfilePictureUrl() == "" || profile.GetProfilePictureExpiresAt() == nil ||
			!profile.GetProfilePictureExpiresAt().AsTime().After(time.Now()) ||
			profile.GetProfilePictureExpiresAt().AsTime().After(time.Now().Add(65*time.Second)) {
			t.Fatalf("invalid short-lived download grant: %+v", profile)
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, profile.GetProfilePictureUrl(), nil)
		if err != nil {
			t.Fatal(err)
		}
		response, err := httpClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = response.Body.Close() }()
		body, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "image/png" ||
			!bytes.Equal(body, png) {
			t.Fatalf(
				"download: status=%d type=%q size=%d",
				response.StatusCode,
				response.Header.Get("Content-Type"),
				len(body),
			)
		}
	}

	for _, invalid := range []*userv1.CreateProfilePictureUploadRequest{
		{ContentType: "image/svg+xml", SizeBytes: int64(len(png))},
		{ContentType: "image/png", SizeBytes: 0},
		{ContentType: "image/png", SizeBytes: service.MaxProfilePictureBytes + 1},
	} {
		if _, err := handler.CreateProfilePictureUpload(
			caller,
			connect.NewRequest(invalid),
		); connect.CodeOf(
			err,
		) != connect.CodeInvalidArgument {
			t.Fatalf("invalid upload metadata accepted (%+v): %v", invalid, err)
		}
	}
	first := upload()
	if _, err := handler.UpdateMyProfilePicture(
		other,
		connect.NewRequest(&userv1.UpdateMyProfilePictureRequest{PictureId: first}),
	); connect.CodeOf(
		err,
	) != connect.CodeFailedPrecondition {
		t.Fatalf("foreign picture apply: %v", err)
	}
	checkDownload(apply(first))
	checkDownload(apply(first)) // Retry after an ambiguous response is idempotent.
	read, err := handler.GetMyProfile(caller, connect.NewRequest(&userv1.GetMyProfileRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	if read.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("profile read can cache a signed URL")
	}
	checkDownload(read.Msg.GetProfile())
	second := upload()
	checkDownload(apply(second))
	var old models.ProfilePicture
	if err := db.First(&old, "id = ?", first).Error; err != nil {
		t.Fatal(err)
	}
	if old.State != models.ProfilePictureDeleting {
		t.Fatalf("replaced picture state = %q", old.State)
	}
	if _, err := storage.Inspect(ctx, old.ObjectKey); err != images.ErrObjectNotFound {
		t.Fatalf("replaced object still available: %v", err)
	}
	for range 2 {
		removed, err := handler.RemoveMyProfilePicture(
			caller,
			connect.NewRequest(&userv1.RemoveMyProfilePictureRequest{}),
		)
		if err != nil {
			t.Fatal(err)
		}
		if removed.Header().Get("Cache-Control") != "no-store" ||
			removed.Msg.GetProfile().GetProfilePictureId() != "" ||
			removed.Msg.GetProfile().GetProfilePictureUrl() != "" {
			t.Fatalf("remove did not clear picture: %+v", removed.Msg)
		}
	}
	var retired models.ProfilePicture
	if err := db.First(&retired, "id = ?", second).Error; err != nil {
		t.Fatal(err)
	}
	if retired.State != models.ProfilePictureDeleting {
		t.Fatalf("removed picture state = %q", retired.State)
	}
	if _, err := storage.Inspect(ctx, retired.ObjectKey); err != images.ErrObjectNotFound {
		t.Fatalf("removed object still available: %v", err)
	}
	third := upload()
	if err := db.Model(&models.User{}).Where("id = ?", user.ID).Update("role", models.RoleSuspended).Error; err != nil {
		t.Fatal(err)
	}
	_, err = handler.UpdateMyProfilePicture(
		caller,
		connect.NewRequest(&userv1.UpdateMyProfilePictureRequest{PictureId: third}),
	)
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("suspended apply: %v", err)
	}
	_, err = handler.CreateProfilePictureUpload(caller, connect.NewRequest(&userv1.CreateProfilePictureUploadRequest{
		ContentType: "image/png",
		SizeBytes:   int64(len(png)),
	}))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("suspended grant: %v", err)
	}
	_, err = handler.RemoveMyProfilePicture(caller, connect.NewRequest(&userv1.RemoveMyProfilePictureRequest{}))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("suspended remove: %v", err)
	}
}
