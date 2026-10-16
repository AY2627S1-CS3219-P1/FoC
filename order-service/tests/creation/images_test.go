//go:build integration

package creation_test

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/images"
	app "github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/order"
	pb "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/order/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/order/v1/orderv1connect"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/google/uuid"
)

func uploadGrant(t *testing.T, f *fixture, c orderv1connect.OrderServiceClient, media string, size int64) *pb.CreateItemImageUploadResponse {
	t.Helper()
	request := connect.NewRequest(&pb.CreateItemImageUploadRequest{ContentType: media, SizeBytes: size})
	request.Header().Set("Authorization", "Bearer user-token")
	response, err := c.CreateItemImageUpload(f.ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	return response.Msg
}

func putImage(t *testing.T, grant *pb.CreateItemImageUploadResponse, body []byte, change func(*http.Request)) int {
	t.Helper()
	request, err := http.NewRequest(http.MethodPut, grant.UploadUrl, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for key, value := range grant.UploadHeaders {
		request.Header.Set(key, value)
	}
	if change != nil {
		change(request)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, response.Body)
	return response.StatusCode
}

func TestItemImagesWithS3(t *testing.T) {
	f := databaseFixture(t)
	storage := imageStorage(t, f.ctx)
	body := []byte("an image")
	scenario := func(t *testing.T) (*scenario, *app.ItemImages, orderv1connect.OrderServiceClient) {
		t.Helper()
		s := newScenario(t, f)
		s.clock.nanos.Store(time.Now().UnixNano())
		images := app.NewItemImages(f.repo, storage)
		s.creator = app.NewCreator(f.repo, s.locations, s.credit, app.CreationOptions{AdmissionsEnabled: true, Now: s.clock.Now, Images: images})
		return s, images, mountImages(t, f, s.creator, images, app.Caller{ID: requester})
	}
	t.Run("five formats attach and retained retries preserve image records", func(t *testing.T) {
		s, _, c := scenario(t)
		in := input(s.clock.Now())
		for _, media := range []string{"image/jpeg", "image/png", "image/webp", "image/heic", "image/heif"} {
			grant := uploadGrant(t, f, c, media, int64(len(body)))
			if putImage(t, grant, body, nil) != http.StatusOK {
				t.Fatal("signed PUT failed")
			}
			in.ItemImageIds = append(in.ItemImageIds, grant.ImageId)
			if putImage(t, grant, body, nil) != http.StatusPreconditionFailed {
				t.Fatal("existing upload was overwritten")
			}
		}
		order, err := create(c, f.ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		replay, err := create(c, f.ctx, in)
		if err != nil || replay.Id != order.Id {
			t.Fatal(err)
		}
		if len(order.ItemImages) != 5 || f.count(t, "order_item_images") != 5 || f.count(t, "orders") != 1 {
			t.Fatal("images were not attached once")
		}
		stored := get(t, c, order.Id)
		if len(stored.ItemImages) != 5 {
			t.Fatal("read omitted attached images")
		}
		for _, image := range stored.ItemImages {
			response, err := http.Get(image.DownloadUrl)
			if err != nil {
				t.Fatal(err)
			}
			content, err := io.ReadAll(response.Body)
			_ = response.Body.Close()
			if err != nil || response.StatusCode != http.StatusOK || !bytes.Equal(content, body) {
				t.Fatal("authorized download failed")
			}
		}
		in.IdempotencyKey = uuid.NewString()
		if _, err := create(c, f.ctx, in); connect.CodeOf(err) != connect.CodeFailedPrecondition {
			t.Fatalf("reused attached images: %v", err)
		}
		if s.remotes.holds != 1 {
			t.Fatal("image reuse created another hold")
		}
	})
	t.Run("signed length and type reject altered body before storage", func(t *testing.T) {
		_, _, c := scenario(t)
		for _, edit := range []func(*http.Request){nil, func(r *http.Request) { r.Header.Set("Content-Type", "image/jpeg") }} {
			grant := uploadGrant(t, f, c, "image/png", int64(len(body)))
			changed := body
			if edit == nil {
				changed = append(append([]byte(nil), body...), 'x')
			}
			if status := putImage(t, grant, changed, edit); status < 400 {
				t.Fatalf("tampered upload accepted: %d", status)
			}
			item, err := f.repo.ItemImage(f.ctx, requester, grant.ImageId)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := storage.Inspect(f.ctx, item.Key); !errors.Is(err, images.ErrObjectNotFound) {
				t.Fatal("invalid body reached storage")
			}
			parsed, err := url.Parse(grant.UploadUrl)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(parsed.Query().Get("X-Amz-SignedHeaders"), "content-length") {
				t.Fatal("size was not signed")
			}
		}
	})
	t.Run("browser preflight and expired signed grants", func(t *testing.T) {
		_, _, c := scenario(t)
		for _, expired := range []bool{false, true} {
			grant := uploadGrant(t, f, c, "image/png", int64(len(body)))
			request, err := http.NewRequest(http.MethodOptions, grant.UploadUrl, nil)
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Origin", "http://localhost:5173")
			request.Header.Set("Access-Control-Request-Method", "PUT")
			request.Header.Set("Access-Control-Request-Headers", "content-type,if-none-match")
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			_ = response.Body.Close()
			if response.StatusCode >= 400 || response.Header.Get("Access-Control-Allow-Origin") != "http://localhost:5173" {
				t.Fatal("browser upload preflight denied")
			}
			// Re-sign the same upload with the known test issuer and an earlier clock.
			// A fresh signature proves the request is valid; an expired one must fail
			// while still inside SigV4's clock-skew window.
			parsed, err := url.Parse(grant.UploadUrl)
			if err != nil {
				t.Fatal(err)
			}
			query := parsed.Query()
			for key := range query {
				if strings.HasPrefix(strings.ToLower(key), "x-amz-") {
					query.Del(key)
				}
			}
			query.Set("X-Amz-Expires", "600")
			parsed.RawQuery = query.Encode()
			put, err := http.NewRequest(http.MethodPut, parsed.String(), bytes.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			for key, value := range grant.UploadHeaders {
				put.Header.Set(key, value)
			}
			signedAt := time.Now()
			if expired {
				signedAt = signedAt.Add(-11 * time.Minute)
			}
			signed, _, err := v4.NewSigner().PresignHTTP(f.ctx, aws.Credentials{AccessKeyID: "test", SecretAccessKey: "test"}, put, "UNSIGNED-PAYLOAD", "s3", "us-east-1", signedAt)
			if err != nil {
				t.Fatal(err)
			}
			altered := *grant
			altered.UploadUrl = signed
			status := putImage(t, &altered, body, nil)
			if (!expired && status != 200) || (expired && status < 400) {
				t.Fatalf("expired=%v upload status=%d", expired, status)
			}
			if expired {
				image, err := f.repo.ItemImage(f.ctx, requester, grant.ImageId)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := storage.Inspect(f.ctx, image.Key); !errors.Is(err, images.ErrObjectNotFound) {
					t.Fatal("expired grant stored an object")
				}
			}
		}
	})

	t.Run("limits ownership missing objects and duplicate IDs", func(t *testing.T) {
		s, _, c := scenario(t)
		for _, size := range []int64{0, app.MaxImageBytes + 1} {
			req := connect.NewRequest(&pb.CreateItemImageUploadRequest{ContentType: "image/png", SizeBytes: size})
			req.Header().Set("Authorization", "Bearer user-token")
			if _, err := c.CreateItemImageUpload(f.ctx, req); connect.CodeOf(err) != connect.CodeInvalidArgument {
				t.Fatal("invalid size accepted")
			}
		}
		if f.count(t, "order_item_images") != 0 {
			t.Fatal("oversize grant persisted")
		}
		for _, size := range []int64{1, app.MaxImageBytes} {
			grant := uploadGrant(t, f, c, "image/png", size)
			if status := putImage(t, grant, make([]byte, size), nil); status != http.StatusOK {
				t.Fatalf("boundary PUT: %d", status)
			}
		}
		grant := uploadGrant(t, f, c, "image/png", int64(len(body)))
		in := input(s.clock.Now())
		in.ItemImageIds = []string{grant.ImageId}
		if _, err := create(c, f.ctx, in); connect.CodeOf(err) != connect.CodeFailedPrecondition {
			t.Fatalf("missing upload admitted: %v", err)
		}
		if putImage(t, grant, body, nil) != 200 {
			t.Fatal("PUT failed")
		}
		other := mountImages(t, f, s.creator, app.NewItemImages(f.repo, storage), app.Caller{ID: uuid.NewString()})
		if _, err := create(other, f.ctx, in); connect.CodeOf(err) != connect.CodeNotFound {
			t.Fatalf("foreign image admitted: %v", err)
		}
		in.ItemImageIds = []string{grant.ImageId, grant.ImageId}
		if _, err := create(c, f.ctx, in); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatal("duplicate image IDs admitted")
		}
		in.ItemImageIds = make([]string, 6)
		for i := range in.ItemImageIds {
			in.ItemImageIds[i] = uuid.NewString()
		}
		if _, err := create(c, f.ctx, in); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatal("six images admitted")
		}
		if f.count(t, "orders") != 0 || s.remotes.holds != 0 {
			t.Fatal("invalid image admission moved money")
		}
	})
	t.Run("two creation keys cannot attach the same upload", func(t *testing.T) {
		s, _, c := scenario(t)
		grant := uploadGrant(t, f, c, "image/png", int64(len(body)))
		if putImage(t, grant, body, nil) != 200 {
			t.Fatal("PUT failed")
		}
		var workers sync.WaitGroup
		results := make(chan error, 2)
		for range 2 {
			workers.Go(func() {
				in := input(s.clock.Now())
				in.ItemImageIds = []string{grant.ImageId}
				_, err := create(c, f.ctx, in)
				results <- err
			})
		}
		workers.Wait()
		close(results)
		wins := 0
		for err := range results {
			if err == nil {
				wins++
			} else if connect.CodeOf(err) != connect.CodeFailedPrecondition {
				t.Fatal(err)
			}
		}
		if wins != 1 || f.count(t, "orders") != 1 || f.count(t, "order_creation_keys") != 1 || s.remotes.holds != 1 {
			t.Fatal("attachment race admitted twice")
		}
	})
	t.Run("failed admission rolls image attachment back", func(t *testing.T) {
		s, _, c := scenario(t)
		grant := uploadGrant(t, f, c, "image/png", int64(len(body)))
		if putImage(t, grant, body, nil) != 200 {
			t.Fatal("PUT failed")
		}
		_, err := f.pool.Exec(f.ctx, `CREATE FUNCTION fail_image_admission() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'test failure'; END $$; CREATE TRIGGER fail_image_admission BEFORE INSERT ON order_creation_workflows FOR EACH ROW EXECUTE FUNCTION fail_image_admission()`)
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			_, _ = f.pool.Exec(f.ctx, "DROP TRIGGER fail_image_admission ON order_creation_workflows; DROP FUNCTION fail_image_admission()")
		}()
		in := input(s.clock.Now())
		in.ItemImageIds = []string{grant.ImageId}
		if _, err := create(c, f.ctx, in); connect.CodeOf(err) != connect.CodeInternal {
			t.Fatalf("admission failure: %v", err)
		}
		image, err := f.repo.ItemImage(f.ctx, requester, grant.ImageId)
		if err != nil || image.State != "uploading" || f.count(t, "orders") != 0 || s.remotes.holds != 0 {
			t.Fatal("partial image/order admission committed")
		}
	})
	t.Run("pending image access and suspended upload authorization", func(t *testing.T) {
		s, service, c := scenario(t)
		s.remotes.unknownReserve = true
		grant := uploadGrant(t, f, c, "image/png", int64(len(body)))
		if putImage(t, grant, body, nil) != 200 {
			t.Fatal("PUT failed")
		}
		in := input(s.clock.Now())
		in.ItemImageIds = []string{grant.ImageId}
		order, err := create(c, f.ctx, in)
		if err != nil || len(order.ItemImages) != 1 {
			t.Fatal(err)
		}
		outsider := mountImages(t, f, s.creator, service, app.Caller{ID: uuid.NewString()})
		req := connect.NewRequest(&pb.GetOrderRequest{Id: order.Id})
		req.Header().Set("Authorization", "Bearer user-token")
		if _, err := outsider.GetOrder(f.ctx, req); connect.CodeOf(err) != connect.CodeNotFound {
			t.Fatal("outsider accessed pending images")
		}
		suspended := mountImages(t, f, s.creator, service, app.Caller{ID: requester, Suspended: true})
		upload := connect.NewRequest(&pb.CreateItemImageUploadRequest{ContentType: "image/png", SizeBytes: 8})
		upload.Header().Set("Authorization", "Bearer user-token")
		if _, err := suspended.CreateItemImageUpload(f.ctx, upload); connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Fatal("suspended user received upload grant")
		}
	})

	t.Run("cleanup removes expired orphans and preserves attached history", func(t *testing.T) {
		s, service, c := scenario(t)
		attached := uploadGrant(t, f, c, "image/png", int64(len(body)))
		orphan := uploadGrant(t, f, c, "image/png", int64(len(body)))
		for _, grant := range []*pb.CreateItemImageUploadResponse{attached, orphan} {
			if putImage(t, grant, body, nil) != 200 {
				t.Fatal("PUT failed")
			}
		}
		in := input(s.clock.Now())
		in.ItemImageIds = []string{attached.ImageId}
		if _, err := create(c, f.ctx, in); err != nil {
			t.Fatal(err)
		}
		row, err := f.repo.ItemImage(f.ctx, requester, orphan.ImageId)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.pool.Exec(f.ctx, "UPDATE order_item_images SET created_at=now()-interval '25 hours',upload_expires_at=now()-interval '24 hours'"); err != nil {
			t.Fatal(err)
		}
		if err := service.CleanupOnce(f.ctx); err != nil {
			t.Fatal(err)
		}
		if f.count(t, "order_item_images") != 1 {
			t.Fatal("cleanup deleted attached history or retained orphan")
		}
		if _, err := storage.Inspect(f.ctx, row.Key); !errors.Is(err, images.ErrObjectNotFound) {
			t.Fatal("orphan object survived cleanup")
		}
	})
}
