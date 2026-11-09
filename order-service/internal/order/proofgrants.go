package order

import (
	"context"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/images"
)

type S3ProofGrants struct{ storage *images.S3 }

func NewS3ProofGrants(storage *images.S3) *S3ProofGrants { return &S3ProofGrants{storage: storage} }

func (s *S3ProofGrants) ProofSigningEpoch(ctx context.Context) (string, time.Time, error) {
	return s.storage.ProofSigningEpoch(ctx)
}

func (s *S3ProofGrants) CreateProofGrant(ctx context.Context, row ProofUpload) (images.UploadGrant, error) {
	return s.storage.CreateFixedUpload(ctx, images.FixedUploadRequest{
		UploadRequest: images.UploadRequest{Key: row.Key, ContentType: row.ContentType, SizeBytes: row.SizeBytes},
		GrantedAt:     row.GrantedAt, ExpiresAt: row.ExpiresAt, SigningEpoch: row.SigningEpoch,
	})
}
