package evidence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/database/evidencedb"
	"github.com/google/uuid"
)

// Repository is the evidence persistence surface outside a submission
// transaction; Attacher runs on the transaction.
type Repository interface {
	InsertStaged(ctx context.Context, input StagedInput) error
	// GetStaged returns the caller's image, or errEvidenceNotFound.
	GetStaged(ctx context.Context, ownerID, id uuid.UUID) (Staged, error)
	ListForParents(ctx context.Context, parents []Parent) ([]Attached, error)
	// Locate returns an attached image's owner, or errEvidenceNotFound.
	Locate(ctx context.Context, id uuid.UUID) (Located, error)
	ClaimOrphans(ctx context.Context, createdBefore, now time.Time) ([]Orphan, error)
	DeleteOrphan(ctx context.Context, id uuid.UUID) error
}

type PostgresRepository struct {
	queries *evidencedb.Queries
}

func NewPostgresRepository(queries *evidencedb.Queries) *PostgresRepository {
	return &PostgresRepository{queries: queries}
}

var _ Repository = (*PostgresRepository)(nil)

func (r *PostgresRepository) InsertStaged(ctx context.Context, input StagedInput) error {
	err := r.queries.InsertEvidence(ctx, evidencedb.InsertEvidenceParams{
		ID:              input.ID,
		OwnerID:         input.OwnerID,
		ObjectKey:       input.ObjectKey,
		ContentType:     input.ContentType,
		SizeBytes:       input.SizeBytes,
		UploadExpiresAt: input.UploadExpiresAt,
	})
	if err != nil {
		return fmt.Errorf("insert staged evidence: %w", err)
	}
	return nil
}

func (r *PostgresRepository) GetStaged(ctx context.Context, ownerID, id uuid.UUID) (Staged, error) {
	row, err := r.queries.GetStagedEvidence(ctx, evidencedb.GetStagedEvidenceParams{ID: id, OwnerID: ownerID})
	if errors.Is(err, sql.ErrNoRows) {
		return Staged{}, errEvidenceNotFound
	}
	if err != nil {
		return Staged{}, fmt.Errorf("get staged evidence: %w", err)
	}
	return Staged{
		Prepared: Prepared{
			ID:          row.ID,
			ObjectKey:   row.ObjectKey,
			ContentType: row.ContentType,
			SizeBytes:   row.SizeBytes,
		},
		State: row.State,
	}, nil
}

func (r *PostgresRepository) ListForParents(ctx context.Context, parents []Parent) ([]Attached, error) {
	params := evidencedb.ListEvidenceForParentsParams{
		ReportIds:         []uuid.UUID{},
		ReportResponseIds: []uuid.UUID{},
		AppealIds:         []uuid.UUID{},
		AppealResponseIds: []uuid.UUID{},
	}
	for _, parent := range parents {
		switch parent.Kind {
		case ParentReport:
			params.ReportIds = append(params.ReportIds, parent.ID)
		case ParentReportResponse:
			params.ReportResponseIds = append(params.ReportResponseIds, parent.ID)
		case ParentAppeal:
			params.AppealIds = append(params.AppealIds, parent.ID)
		case ParentAppealResponse:
			params.AppealResponseIds = append(params.AppealResponseIds, parent.ID)
		}
	}

	rows, err := r.queries.ListEvidenceForParents(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("list evidence: %w", err)
	}
	out := make([]Attached, len(rows))
	for i, row := range rows {
		out[i] = Attached{
			ID:          row.ID,
			ObjectKey:   row.ObjectKey,
			ContentType: row.ContentType,
			Parent:      parentOf(row),
		}
	}
	return out, nil
}

func (r *PostgresRepository) Locate(ctx context.Context, id uuid.UUID) (Located, error) {
	row, err := r.queries.LocateEvidence(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return Located{}, errEvidenceNotFound
	}
	if err != nil {
		return Located{}, fmt.Errorf("locate evidence: %w", err)
	}
	return Located{
		Owner:       Owner{ReportID: row.OwningReportID, AppealID: row.OwningAppealID},
		ObjectKey:   row.ObjectKey,
		ContentType: row.ContentType,
	}, nil
}

func (r *PostgresRepository) ClaimOrphans(ctx context.Context, createdBefore, now time.Time) ([]Orphan, error) {
	rows, err := r.queries.ClaimOrphanEvidence(ctx, evidencedb.ClaimOrphanEvidenceParams{
		CreatedBefore: createdBefore,
		NowAt:         now,
	})
	if err != nil {
		return nil, err
	}
	out := make([]Orphan, len(rows))
	for i, row := range rows {
		out[i] = Orphan{ID: row.ID, ObjectKey: row.ObjectKey}
	}
	return out, nil
}

func (r *PostgresRepository) DeleteOrphan(ctx context.Context, id uuid.UUID) error {
	_, err := r.queries.DeleteOrphanEvidence(ctx, id)
	return err
}

func parentOf(row evidencedb.ListEvidenceForParentsRow) Parent {
	switch {
	case row.ReportID.Valid:
		return ReportParent(row.ReportID.UUID)
	case row.ReportResponseID.Valid:
		return ReportResponseParent(row.ReportResponseID.UUID)
	case row.AppealID.Valid:
		return AppealParent(row.AppealID.UUID)
	default:
		return AppealResponseParent(row.AppealResponseID.UUID)
	}
}

// Attacher attaches prepared images on a submission's transaction.
type Attacher struct {
	queries *evidencedb.Queries
}

// NewAttacher binds an Attacher to db, which must be the submission's
// transaction.
func NewAttacher(db evidencedb.DBTX) *Attacher {
	return &Attacher{queries: evidencedb.New(db)}
}

// Attach attaches the prepared images to parent in order. An image that
// changed since it was prepared fails the submission.
func (a *Attacher) Attach(ctx context.Context, ownerID uuid.UUID, parent Parent, prepared []Prepared) error {
	for ordinal, image := range prepared {
		params := evidencedb.AttachEvidenceParams{
			Ordinal:     sql.NullInt32{Int32: int32(ordinal), Valid: true},
			ID:          image.ID,
			OwnerID:     ownerID,
			ObjectKey:   image.ObjectKey,
			ContentType: image.ContentType,
			SizeBytes:   image.SizeBytes,
		}
		id := uuid.NullUUID{UUID: parent.ID, Valid: true}
		switch parent.Kind {
		case ParentReport:
			params.ReportID = id
		case ParentReportResponse:
			params.ReportResponseID = id
		case ParentAppeal:
			params.AppealID = id
		case ParentAppealResponse:
			params.AppealResponseID = id
		default:
			return fmt.Errorf("attach evidence: unknown parent kind %d", parent.Kind)
		}

		rows, err := a.queries.AttachEvidence(ctx, params)
		if err != nil {
			return fmt.Errorf("attach evidence: %w", err)
		}
		if rows != 1 {
			return notReady(image.ID, "an image is no longer available; remove it and submit again")
		}
	}
	return nil
}
