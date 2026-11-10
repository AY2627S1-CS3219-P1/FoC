// Service constructors live here so main stays a composition root.
package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/rs/cors"
	"gorm.io/gorm"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth/httpauth"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/images"
	allowedadmin "github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/admin"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/appeal"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/communication"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/database/alloweddomaindb"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/database/appealdb"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/database/evidencedb"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/database/reportdb"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/database/reviewdb"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/database/warningdb"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/evidence"
	authhandler "github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/handlers/auth"
	userservicejwt "github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/jwt"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/report"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/reportoutcome"
	review "github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/review"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/service"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/store"
	warningdomain "github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/warning"
)

// Communication sends magic links synchronously; its SMTP exchange is
// bounded at 5s by default.
const magicLinkTimeout = 15 * time.Second

// newReviewService builds the review domain; photo storage is optional.
func newReviewService(sqlDB *sql.DB, cfg config) (*review.Service, *images.S3) {
	repo := review.NewPostgresRepository(reviewdb.New(sqlDB))
	var photos *images.S3
	if cfg.s3Bucket != "" {
		storage, err := images.NewS3(context.Background(), images.S3Config{
			Bucket:         cfg.s3Bucket,
			Endpoint:       cfg.s3Endpoint,
			PublicEndpoint: cfg.s3PublicEndpoint,
			UploadTTL:      cfg.s3UploadTTL,
			DownloadTTL:    cfg.s3DownloadTTL,
			Region:         cfg.awsRegion,
		})
		if err != nil {
			slog.Error("review photo storage disabled: invalid configuration", "err", err)
		} else {
			photos = storage
		}
	}
	// A typed-nil *images.S3 would bypass the domain's availability checks.
	var reviewStorage review.PhotoStorage
	if photos != nil {
		reviewStorage = photos
	}
	service := review.NewService(repo, func(ctx context.Context, operation func(*reviewdb.Queries) error) error {
		tx, err := sqlDB.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback() }()
		if err := operation(reviewdb.New(tx)); err != nil {
			return err
		}
		return tx.Commit()
	}, reviewStorage)
	return service, photos
}

// newEvidenceService stores report and appeal images in the review photo
// bucket under evidence/. storage is nil without a bucket.
func newEvidenceService(sqlDB *sql.DB, storage *images.S3) *evidence.Service {
	// A typed-nil *images.S3 would bypass the domain's availability checks.
	var evidenceStorage evidence.Storage
	if storage != nil {
		evidenceStorage = storage
	}
	return evidence.NewService(evidence.NewPostgresRepository(evidencedb.New(sqlDB)), evidenceStorage)
}

// newReportService reads through the shared pool and closes reports in a GORM
// transaction, so outcomes reuse the role, warning and review domains.
func newReportService(db *gorm.DB, sqlDB *sql.DB, images *evidence.Service) *report.Service {
	repo := report.NewPostgresRepository(reportdb.New(sqlDB))
	return report.NewService(repo, reportoutcome.WithTransaction(db), images)
}

// newAppealService closes appeals in a GORM transaction, so overturned
// outcomes reuse the same domains as report outcomes.
func newAppealService(db *gorm.DB, sqlDB *sql.DB, images *evidence.Service) *appeal.Service {
	repo := appeal.NewPostgresRepository(appealdb.New(sqlDB))
	return appeal.NewService(repo, reportoutcome.WithAppealTransaction(db), images)
}

func newWarningService(db warningdb.DBTX) *warningdomain.Service {
	return warningdomain.NewService(warningdomain.NewPostgresRepository(warningdb.New(db)))
}

func newAllowedDomainService(db alloweddomaindb.DBTX) *allowedadmin.AllowedDomainService {
	var repo allowedadmin.AllowedDomainRepository = allowedadmin.NewPostgresAllowedAdminRepository(alloweddomaindb.New(db))
	return allowedadmin.NewAllowedDomainService(repo)
}

func authResources(persistence *store.Store, policy service.EmailDomainPolicy) service.Resources {
	return service.Resources{
		Users:             persistence.Users,
		AuthTokens:        persistence.AuthTokens,
		Sessions:          persistence.Sessions,
		DomainPolicy:      policy,
		UserCreatedEvents: persistence.UserCreatedOutbox,
	}
}

func authTransactions(db *gorm.DB) service.WithTransaction {
	return func(ctx context.Context, operation func(service.Resources) error) error {
		return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			// Bind sqlc and GORM to the same transaction, without acquiring another connection.
			policy := newAllowedDomainService(tx.Statement.ConnPool)
			return operation(authResources(store.New(tx), policy))
		})
	}
}

func newAuthServices(
	db *gorm.DB,
	cfg config,
	domainPolicy service.EmailDomainPolicy,
) (*authhandler.Handler, *userservicejwt.ES256Codec, *store.Store, error) {
	frontendURL, err := url.Parse(cfg.frontendURL)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("parse FRONTEND_BASE_URL: %w", err)
	}
	key, err := userservicejwt.LoadPrivateKeyPEM(cfg.privateKey)
	if err != nil {
		return nil, nil, nil, err
	}
	kid, err := keyID(&key.PublicKey)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("encode JWT public key: %w", err)
	}
	codec, err := userservicejwt.NewES256Codec(key, kid,
		httpauth.TokenIssuer, httpauth.TokenAudience)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("configure JWT signing: %w", err)
	}
	persistence := store.New(db)

	logic, err := service.NewService(service.Dependencies{
		Resources: authResources(persistence, domainPolicy), WithTransaction: authTransactions(db),
		TokenCodec: codec,
		MagicLinks: communication.NewMagicLinks(&http.Client{Timeout: magicLinkTimeout}, cfg.communicationURL),
	}, service.Config{
		FrontendBaseURL: *frontendURL, LocalDevelopment: cfg.appEnv == "local",
		AccessTokenTTL: cfg.accessTokenTTL, RefreshTokenTTL: cfg.refreshTokenTTL,
	})
	if err != nil {
		return nil, nil, nil, fmt.Errorf("configure auth service: %w", err)
	}
	allowedOrigin := frontendURL.Scheme + "://" + frontendURL.Host
	return &authhandler.Handler{Logic: logic, AllowedOrigin: allowedOrigin}, codec, persistence, nil
}

func keyID(key *ecdsa.PublicKey) (string, error) {
	point, err := key.Bytes()
	if err != nil {
		return "", err
	}
	fingerprint := sha256.Sum256(point)
	return hex.EncodeToString(fingerprint[:]), nil
}

func getCorsConfig(allowedOrigin string) *cors.Cors {
	return cors.New(cors.Options{
		AllowedOrigins:   []string{allowedOrigin},
		AllowCredentials: true,
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders: []string{
			"Authorization",
			"Content-Type",
			"Connect-Protocol-Version",
			"Connect-Timeout-Ms",
			"Grpc-Timeout",
			"X-Grpc-Web",
			"X-User-Agent",
		},
		ExposedHeaders: []string{
			"Grpc-Status",
			"Grpc-Message",
			"Grpc-Status-Details-Bin",
		},
	})
}
