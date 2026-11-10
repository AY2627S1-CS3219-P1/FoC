//go:build integration

// Exercise authorization through an in-process Connect client.
package router_test

import (
	"context"
	moderationjob "github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/job"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"connectrpc.com/connect"
	"connectrpc.com/grpchealth"
	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/database"
	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/moderation"
	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/router"
	reviewrpc "github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/rpc/review"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth"
	reviewv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/moderation/review/v1"
	reviewv1connect "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/moderation/review/v1/reviewv1connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type muxTransport struct{ handler http.Handler }

func (m muxTransport) Do(r *http.Request) (*http.Response, error) {
	rec := httptest.NewRecorder()
	m.handler.ServeHTTP(rec, r)
	return rec.Result(), nil
}

func newMux(
	t *testing.T,
	pool *pgxpool.Pool,
	caller *auth.Caller,
) (*http.ServeMux, reviewv1connect.ReviewModerationServiceClient, reviewv1connect.ReviewModerationAdminServiceClient) {
	t.Helper()
	service := moderationjob.NewService(pool, moderationjob.DefaultRetrySettings(), nil, "test-model")
	services := router.RPCServices{
		Health:                grpchealth.NewStaticChecker(),
		ReviewModeration:      reviewrpc.NewOwnerHandler(service),
		ReviewModerationAdmin: reviewrpc.NewAdminHandler(service),
	}
	var authenticate func(http.Handler) http.Handler
	if caller == nil {
		authenticate = func(next http.Handler) http.Handler { return next }
	} else {
		authenticate = func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				next.ServeHTTP(w, r.WithContext(auth.WithCaller(r.Context(), *caller)))
			})
		}
	}
	mux := router.Public(services, authenticate)
	base := "http://test"
	return mux,
		reviewv1connect.NewReviewModerationServiceClient(muxTransport{handler: mux}, base),
		reviewv1connect.NewReviewModerationAdminServiceClient(muxTransport{handler: mux}, base)
}

func completedJobFor(t *testing.T, pool *pgxpool.Pool, reviewerID uuid.UUID) (uuid.UUID, uuid.UUID) {
	t.Helper()
	ctx := context.Background()

	in := moderationjob.IngestionInput{
		SourceEventID:       uuid.New(),
		ModerationRequestID: uuid.New(),
		ReviewID:            uuid.New(),
		ReviewerID:          reviewerID,
		ContentRevision:     1,
		Comment:             "abac target",
	}
	jobStore := moderationjob.NewService(pool, moderationjob.DefaultRetrySettings(), nil, "test-model")
	if _, err := jobStore.Ingest(ctx, in); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	_, claimed, err := jobStore.Claim(ctx, "abac-worker", 30*time.Second)
	if err != nil || claimed == nil {
		t.Fatalf("claim: %v %v", claimed, err)
	}
	if err := jobStore.Complete(ctx, *claimed, moderation.ReviewAssessment{
		Outcome: moderation.OutcomeApproved, PolicyVersion: "review-text-v1",
	}); err != nil {
		t.Fatalf("complete: %v", err)
	}
	return claimed.Job.ID, in.ModerationRequestID
}

func setupDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping integration test")
	}
	ctx := context.Background()
	if err := database.Migrate(ctx, url); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	pool := database.Connect(url)
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(ctx, `
		TRUNCATE moderation_jobs, moderation_attempts, moderation_outbox,
			moderation_quarantine, moderation_admin_audit`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	return pool
}

func TestInternalListenerServesOnlyHealth(t *testing.T) {
	pool := setupDB(t)
	service := moderationjob.NewService(pool, moderationjob.DefaultRetrySettings(), nil, "test-model")
	services := router.RPCServices{
		Health:                grpchealth.NewStaticChecker(),
		ReviewModeration:      reviewrpc.NewOwnerHandler(service),
		ReviewModerationAdmin: reviewrpc.NewAdminHandler(service),
	}
	mux := router.Internal(services)
	client := reviewv1connect.NewReviewModerationServiceClient(muxTransport{handler: mux}, "http://test")
	_, err := client.GetReviewModerationJobsForReviewer(context.Background(),
		connect.NewRequest(&reviewv1.GetReviewModerationJobsForReviewerRequest{}))
	if connect.CodeOf(err) != connect.CodeUnimplemented {
		t.Fatalf("internal admin RPC code = %v, want unimplemented (not mounted)", connect.CodeOf(err))
	}
}

func TestOwnerServiceCallerGating(t *testing.T) {
	pool := setupDB(t)
	owner := uuid.New()
	_, requestID := completedJobFor(t, pool, owner)
	other := uuid.New()

	t.Run("unauthenticated caller is rejected", func(t *testing.T) {
		mux, client, _ := newMux(t, pool, nil)
		_ = mux
		_, err := client.GetReviewModerationJobsForReviewer(context.Background(),
			connect.NewRequest(&reviewv1.GetReviewModerationJobsForReviewerRequest{
				ReviewerId:           owner.String(),
				ModerationRequestIds: []string{requestID.String()},
			}))
		if connect.CodeOf(err) != connect.CodeUnauthenticated {
			t.Fatalf("code = %v, want unauthenticated", connect.CodeOf(err))
		}
	})

	t.Run("ordinary caller may only query their own reviewer_id", func(t *testing.T) {
		_, client, _ := newMux(t, pool, &auth.Caller{ID: other.String()})
		_, err := client.GetReviewModerationJobsForReviewer(context.Background(),
			connect.NewRequest(&reviewv1.GetReviewModerationJobsForReviewerRequest{
				ReviewerId:           owner.String(),
				ModerationRequestIds: []string{requestID.String()},
			}))
		if connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Fatalf("code = %v, want permission_denied", connect.CodeOf(err))
		}
	})

	t.Run("owner query returns their job", func(t *testing.T) {
		_, client, _ := newMux(t, pool, &auth.Caller{ID: owner.String()})
		resp, err := client.GetReviewModerationJobsForReviewer(context.Background(),
			connect.NewRequest(&reviewv1.GetReviewModerationJobsForReviewerRequest{
				ReviewerId:           owner.String(),
				ModerationRequestIds: []string{requestID.String()},
			}))
		if err != nil {
			t.Fatalf("owner query: %v", err)
		}
		if len(resp.Msg.Jobs) != 1 || resp.Msg.Jobs[0].ModerationRequestId != requestID.String() {
			t.Fatalf("jobs = %+v, want the one completed job", resp.Msg.Jobs)
		}
		if got := resp.Msg.Jobs[0].Outcome; got != reviewv1.ModerationOutcomeType_MODERATION_OUTCOME_TYPE_APPROVED {
			t.Fatalf("outcome = %v, want approved", got)
		}
	})

	t.Run("admin may query any reviewer", func(t *testing.T) {
		_, client, _ := newMux(t, pool, &auth.Caller{ID: "admin-1", Admin: true})
		resp, err := client.GetReviewModerationJobsForReviewer(context.Background(),
			connect.NewRequest(&reviewv1.GetReviewModerationJobsForReviewerRequest{
				ReviewerId:           owner.String(),
				ModerationRequestIds: []string{requestID.String()},
			}))
		if err != nil {
			t.Fatalf("admin query: %v", err)
		}
		if len(resp.Msg.Jobs) != 1 {
			t.Fatalf("jobs = %d, want 1", len(resp.Msg.Jobs))
		}
	})

	t.Run("more than 100 request ids is rejected by validation", func(t *testing.T) {
		_, client, _ := newMux(t, pool, &auth.Caller{ID: owner.String()})
		ids := make([]string, 0, 101)
		for range 101 {
			ids = append(ids, uuid.NewString())
		}
		_, err := client.GetReviewModerationJobsForReviewer(context.Background(),
			connect.NewRequest(&reviewv1.GetReviewModerationJobsForReviewerRequest{
				ReviewerId:           owner.String(),
				ModerationRequestIds: ids,
			}))
		if connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatalf("code = %v, want invalid_argument", connect.CodeOf(err))
		}
	})
}

func TestAdminServiceGating(t *testing.T) {
	pool := setupDB(t)
	owner := uuid.New()
	jobID, _ := completedJobFor(t, pool, owner)

	t.Run("unauthenticated caller is rejected", func(t *testing.T) {
		_, _, admin := newMux(t, pool, nil)
		_, err := admin.GetReviewModerationJob(context.Background(),
			connect.NewRequest(&reviewv1.GetReviewModerationJobRequest{JobId: jobID.String()}))
		if connect.CodeOf(err) != connect.CodeUnauthenticated {
			t.Fatalf("code = %v, want unauthenticated", connect.CodeOf(err))
		}
	})

	t.Run("non-admin caller is rejected before any store access", func(t *testing.T) {
		_, _, admin := newMux(t, pool, &auth.Caller{ID: owner.String()})
		_, err := admin.GetReviewModerationJob(context.Background(),
			connect.NewRequest(&reviewv1.GetReviewModerationJobRequest{JobId: jobID.String()}))
		if connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Fatalf("code = %v, want permission_denied", connect.CodeOf(err))
		}
	})

	t.Run("admin reads bounded job detail", func(t *testing.T) {
		_, _, admin := newMux(t, pool, &auth.Caller{ID: "admin-1", Admin: true})
		resp, err := admin.GetReviewModerationJob(context.Background(),
			connect.NewRequest(&reviewv1.GetReviewModerationJobRequest{JobId: jobID.String()}))
		if err != nil {
			t.Fatalf("admin job read: %v", err)
		}
		if resp.Msg.Job.JobId != jobID.String() {
			t.Fatalf("job id = %q, want %q", resp.Msg.Job.JobId, jobID)
		}
	})

	t.Run("unknown job is not found for an admin", func(t *testing.T) {
		_, _, admin := newMux(t, pool, &auth.Caller{ID: "admin-1", Admin: true})
		_, err := admin.GetReviewModerationJob(context.Background(),
			connect.NewRequest(&reviewv1.GetReviewModerationJobRequest{JobId: uuid.NewString()}))
		if connect.CodeOf(err) != connect.CodeNotFound {
			t.Fatalf("code = %v, want not_found", connect.CodeOf(err))
		}
	})
}

func TestRetryErroredJobGenerationFencing(t *testing.T) {
	pool := setupDB(t)
	owner := uuid.New()
	jobID, _ := completedJobFor(t, pool, owner)
	ctx := context.Background()

	if _, err := pool.Exec(ctx, `
		UPDATE moderation_jobs
		SET status = 'errored', error_class = 'connection', outcome = NULL,
		    finished_at = now(), claim_token = NULL,
		    lease_until = NULL, worker_id = NULL
		WHERE id = $1`, jobID); err != nil {
		t.Fatalf("force errored: %v", err)
	}

	_, _, admin := newMux(t, pool, &auth.Caller{ID: "admin-1", Admin: true})

	t.Run("generation mismatch fails precondition", func(t *testing.T) {
		_, err := admin.RetryErroredJob(ctx, connect.NewRequest(&reviewv1.RetryErroredJobRequest{
			JobId:                 jobID.String(),
			ExpectedRunGeneration: 99,
		}))
		if connect.CodeOf(err) != connect.CodeFailedPrecondition {
			t.Fatalf("code = %v, want failed_precondition", connect.CodeOf(err))
		}
	})

	t.Run("matching generation requeues exactly once", func(t *testing.T) {
		resp, err := admin.RetryErroredJob(ctx, connect.NewRequest(&reviewv1.RetryErroredJobRequest{
			JobId:                 jobID.String(),
			ExpectedRunGeneration: 1,
		}))
		if err != nil {
			t.Fatalf("retry: %v", err)
		}
		if got := resp.Msg.Job.RunGeneration; got != 2 {
			t.Fatalf("run_generation = %d, want 2", got)
		}
		var status string
		if err := pool.QueryRow(ctx,
			`SELECT status FROM moderation_jobs WHERE id = $1`, jobID).Scan(&status); err != nil {
			t.Fatalf("load job: %v", err)
		}
		if status != "queued" {
			t.Fatalf("status = %q, want queued", status)
		}
	})

	t.Run("retried job is no longer errored so retry fails again", func(t *testing.T) {
		_, err := admin.RetryErroredJob(ctx, connect.NewRequest(&reviewv1.RetryErroredJobRequest{
			JobId:                 jobID.String(),
			ExpectedRunGeneration: 2,
		}))
		if connect.CodeOf(err) != connect.CodeFailedPrecondition {
			t.Fatalf("code = %v, want failed_precondition (queued job is not retriable)", connect.CodeOf(err))
		}
	})
}
