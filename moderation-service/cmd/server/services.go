package main

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/moderation-service/internal/appeal"
	"github.com/AY2627S1-CS3219-P1/FoC/moderation-service/internal/database/appealdb"
	"github.com/AY2627S1-CS3219-P1/FoC/moderation-service/internal/database/evidencedb"
	"github.com/AY2627S1-CS3219-P1/FoC/moderation-service/internal/database/reportdb"
	"github.com/AY2627S1-CS3219-P1/FoC/moderation-service/internal/evidence"
	"github.com/AY2627S1-CS3219-P1/FoC/moderation-service/internal/health"
	"github.com/AY2627S1-CS3219-P1/FoC/moderation-service/internal/orders"
	"github.com/AY2627S1-CS3219-P1/FoC/moderation-service/internal/outbox"
	"github.com/AY2627S1-CS3219-P1/FoC/moderation-service/internal/report"
	"github.com/AY2627S1-CS3219-P1/FoC/moderation-service/internal/router"
	"github.com/AY2627S1-CS3219-P1/FoC/moderation-service/internal/users"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/order/v1/orderv1connect"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/v1/userv1connect"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/images"
)

// serviceCallTimeout bounds each call to User or Order Service.
const serviceCallTimeout = 5 * time.Second

// services is the composition root's output.
type services struct {
	rpc      router.Services
	evidence *evidence.Service
	// storage is nil when no evidence bucket is configured.
	storage *images.S3
}

func newServices(ctx context.Context, db *sql.DB, c config) (services, error) {
	client := &http.Client{Timeout: serviceCallTimeout}
	directory := users.NewClient(userv1connect.NewUserInternalServiceClient(client, c.UserServiceURL))

	storage, err := newEvidenceStorage(ctx, c)
	if err != nil {
		return services{}, err
	}
	evidenceLogic := newEvidenceService(db, storage)

	reportLogic := report.NewService(
		report.NewPostgresRepository(reportdb.New(db)),
		outbox.ReportTransaction(db),
		evidenceLogic,
		newOrderGateway(c, client),
		directory,
	)
	appealLogic := appeal.NewService(
		appeal.NewPostgresRepository(appealdb.New(db)),
		outbox.AppealTransaction(db),
		evidenceLogic,
		directory,
	)

	return services{
		rpc: router.Services{
			Health: &health.Handler{DB: db},
			Report: &report.Handler{Logic: reportLogic},
			Appeal: &appeal.Handler{Logic: appealLogic},
		},
		evidence: evidenceLogic,
		storage:  storage,
	}, nil
}

// newOrderGateway reads order participants from Order's internal listener, and
// fails closed while ORDER_SERVICE_BASE_URL is unset.
func newOrderGateway(c config, client *http.Client) orders.Gateway {
	if c.OrderServiceURL == "" {
		slog.Warn("ORDER_SERVICE_BASE_URL is not set; order report checks fail closed")
		return orders.UnavailableGateway{}
	}
	return orders.NewConnectGateway(orderv1connect.NewOrderInternalServiceClient(client, c.OrderServiceURL))
}

func newEvidenceStorage(ctx context.Context, c config) (*images.S3, error) {
	if c.S3Bucket == "" {
		slog.Warn("MODERATION_S3_BUCKET is not set; evidence uploads are unavailable")
		return nil, nil
	}
	storage, err := images.NewS3(ctx, images.S3Config{
		Bucket:         c.S3Bucket,
		Endpoint:       c.S3Endpoint,
		PublicEndpoint: c.S3PublicEndpoint,
		UploadTTL:      c.S3UploadTTL,
		DownloadTTL:    c.S3DownloadTTL,
		Region:         c.AWSRegion,
	})
	if err != nil {
		return nil, fmt.Errorf("configure evidence storage: %w", err)
	}
	return storage, nil
}

// newEvidenceService avoids passing a typed-nil storage, which would bypass
// the domain's availability checks.
func newEvidenceService(db *sql.DB, storage *images.S3) *evidence.Service {
	var evidenceStorage evidence.Storage
	if storage != nil {
		evidenceStorage = storage
	}
	return evidence.NewService(evidence.NewPostgresRepository(evidencedb.New(db)), evidenceStorage)
}
