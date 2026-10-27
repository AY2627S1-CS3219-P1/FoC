package images

import (
	"context"
	"errors"
	"testing"
	"time"
)

// fakeStorage exists only in tests. Production composition never receives it.
type fakeStorage struct {
	objects map[string]ObjectInfo
	fail    error
}

func (f *fakeStorage) CreateUpload(ctx context.Context, req UploadRequest) (UploadGrant, error) {
	if err := ctx.Err(); err != nil {
		return UploadGrant{}, err
	}
	if f.fail != nil {
		return UploadGrant{}, f.fail
	}
	return UploadGrant{
		URL:       "https://upload.test/temporary",
		Method:    "PUT",
		Headers:   map[string]string{"Content-Type": req.ContentType},
		ExpiresAt: time.Now().Add(time.Minute),
	}, nil
}

func (f *fakeStorage) Inspect(ctx context.Context, key string) (ObjectInfo, error) {
	if err := ctx.Err(); err != nil {
		return ObjectInfo{}, err
	}
	if f.fail != nil {
		return ObjectInfo{}, f.fail
	}
	info, ok := f.objects[key]
	if !ok {
		return ObjectInfo{}, ErrObjectNotFound
	}
	info.Exists = true
	return info, nil
}

func (f *fakeStorage) CreateDownload(ctx context.Context, key string) (DownloadGrant, error) {
	if err := ctx.Err(); err != nil {
		return DownloadGrant{}, err
	}
	if f.fail != nil {
		return DownloadGrant{}, f.fail
	}
	if _, ok := f.objects[key]; !ok {
		return DownloadGrant{}, ErrObjectNotFound
	}
	return DownloadGrant{URL: "https://download.test/temporary", ExpiresAt: time.Now().Add(time.Minute)}, nil
}

func (f *fakeStorage) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if f.fail != nil {
		return f.fail
	}
	delete(f.objects, key)
	return nil
}

func TestFakeStorageSemantics(t *testing.T) {
	f := &fakeStorage{objects: map[string]ObjectInfo{"order/one/front": {Size: 42, ContentType: "image/jpeg"}}}
	info, err := f.Inspect(context.Background(), "order/one/front")
	if err != nil || !info.Exists || info.Size != 42 || info.ContentType != "image/jpeg" {
		t.Fatalf("Inspect = (%+v, %v)", info, err)
	}
	if _, err := f.Inspect(context.Background(), "missing"); !errors.Is(err, ErrObjectNotFound) {
		t.Fatalf("missing Inspect error = %v", err)
	}
	fail := errors.New("storage unavailable")
	f.fail = fail
	if _, err := f.Inspect(
		context.Background(),
		"order/one/front",
	); !errors.Is(err, fail) ||
		errors.Is(err, ErrObjectNotFound) {
		t.Fatalf("storage failure = %v", err)
	}
	f.fail = nil
	if err := f.Delete(context.Background(), "missing"); err != nil {
		t.Fatalf("Delete missing object: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := f.CreateUpload(
		ctx,
		UploadRequest{Key: "stable/key", ContentType: "image/png"},
	); !errors.Is(
		err,
		context.Canceled,
	) {
		t.Fatalf("CreateUpload canceled context error = %v", err)
	}
}
