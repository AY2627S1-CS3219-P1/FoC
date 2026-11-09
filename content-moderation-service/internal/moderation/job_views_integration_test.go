//go:build integration

package moderation_test

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"testing"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/moderation"
)

func TestJobViewsReportEffectiveConfigurationPause(t *testing.T) {
	for _, endpointChange := range []bool{false, true} {
		t.Run(
			map[bool]string{false: "sibling configuration failure", true: "endpoint changed"}[endpointChange],
			func(t *testing.T) {
				ctx := context.Background()
				pool := newTestPool(t)
				original := activateTestConfig(t, pool)
				options := []moderation.JobOption{}
				if endpointChange {
					options = append(options, moderation.WithProviderIdentity("https://changed.example/v1"))
				}
				jobs := moderation.NewJobService(pool, moderation.DefaultRetrySettings(), nil, options...)
				first, second := ingestInput("first"), ingestInput("second")
				for _, input := range []moderation.IngestionInput{first, second} {
					if _, err := jobs.Ingest(ctx, input); err != nil {
						t.Fatal(err)
					}
				}
				outcome, claimed, err := jobs.Claim(ctx, "test", time.Minute)
				if err != nil {
					t.Fatal(err)
				}
				if endpointChange {
					if outcome != moderation.ClaimNoConfiguration {
						t.Fatalf("claim = %v", outcome)
					}
				} else {
					if outcome != moderation.ClaimReady {
						t.Fatalf("claim = %v", outcome)
					}
					if err := jobs.FinalizeFailed(
						ctx,
						*claimed,
						moderation.WrapConfigurationError(errors.New("bad model")),
					); err != nil {
						t.Fatal(err)
					}
				}
				id := jobIDByRequest(t, pool, second.ModerationRequestID)
				assertPaused := func(want bool) {
					t.Helper()
					view, err := jobs.GetJobViewByID(ctx, id)
					if err != nil || view.ConfigurationPaused != want {
						t.Fatalf("view = %+v, err = %v, want paused %v", view, err, want)
					}
					views, err := jobs.GetJobsForReviewer(
						ctx,
						second.ReviewerID,
						[]uuid.UUID{second.ModerationRequestID},
					)
					if err != nil || len(views) != 1 || views[0].ConfigurationPaused != want {
						t.Fatalf("views = %+v, err = %v", views, err)
					}
				}
				assertPaused(true)
				configs := moderation.NewConfigurationStore(pool)
				next, err := configs.InsertConfigVersion(
					ctx,
					"fixed",
					[]byte(`{}`),
					moderation.OutputModeJSONSchema,
					"review-text-v1",
					"fixed-fingerprint",
					"tester",
					"",
				)
				if err != nil {
					t.Fatal(err)
				}
				if err := configs.ActivateConfigVersion(ctx, next, "tester", &original); err != nil {
					t.Fatal(err)
				}
				assertPaused(false)
				if _, err := pool.Exec(
					ctx,
					`UPDATE moderation_jobs SET config_version_id=$2 WHERE id=$1`,
					id,
					original.String(),
				); err != nil {
					t.Fatal(err)
				}
				assertPaused(true)
				if _, err := pool.Exec(ctx, `UPDATE moderation_jobs SET status='errored' WHERE id=$1`, id); err != nil {
					t.Fatal(err)
				}
				assertPaused(false)
				if _, err := pool.Exec(ctx, `UPDATE moderation_jobs SET status='queued' WHERE id=$1`, id); err != nil {
					t.Fatal(err)
				}
				if err := configs.ActivateAndResume(ctx, next, "tester", &next, true); err != nil {
					t.Fatal(err)
				}
				assertPaused(false)
			},
		)
	}
}
