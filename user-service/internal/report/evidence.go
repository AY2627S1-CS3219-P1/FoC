package report

import (
	"context"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api/errs"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/images"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/evidence"
	"github.com/google/uuid"
)

// Evidence stages, checks and signs report images (RP1.6, RP2.2).
type Evidence interface {
	CreateUpload(ctx context.Context, ownerID uuid.UUID, contentType string, sizeBytes int64) (
		uuid.UUID, images.UploadGrant, error)
	// Prepare inspects storage, so it runs before the submission transaction.
	Prepare(ctx context.Context, ownerID uuid.UUID, ids []uuid.UUID) ([]evidence.Prepared, error)
	Views(ctx context.Context, parents []evidence.Parent) (map[evidence.Parent][]evidence.View, error)
	Locate(ctx context.Context, id uuid.UUID) (evidence.Owner, error)
	Download(ctx context.Context, id uuid.UUID) (evidence.View, error)
}

// EvidenceAttacher attaches prepared images on the submission transaction.
type EvidenceAttacher interface {
	Attach(ctx context.Context, ownerID uuid.UUID, parent evidence.Parent, prepared []evidence.Prepared) error
}

// CreateEvidenceUpload grants a direct upload of an image the caller may then
// attach to a report or response.
func (s *Service) CreateEvidenceUpload(
	ctx context.Context,
	ownerID uuid.UUID,
	contentType string,
	sizeBytes int64,
) (uuid.UUID, images.UploadGrant, error) {
	return s.evidence.CreateUpload(ctx, ownerID, contentType, sizeBytes)
}

// GetEvidenceDownload signs a fresh URL for an image on a report the caller
// can read, or on one of its responses.
func (s *Service) GetEvidenceDownload(
	ctx context.Context,
	callerID uuid.UUID,
	isAdmin bool,
	evidenceID uuid.UUID,
) (evidence.View, error) {
	owner, err := s.evidence.Locate(ctx, evidenceID)
	if err != nil {
		return evidence.View{}, err
	}
	if owner.ReportID == uuid.Nil {
		return evidence.View{}, errs.NewNotFoundError("evidence not found")
	}
	if _, err := s.GetReport(ctx, callerID, isAdmin, owner.ReportID); err != nil {
		return evidence.View{}, err
	}
	return s.evidence.Download(ctx, evidenceID)
}

// withEvidence adds the attached images to reports and their responses.
func (s *Service) withEvidence(ctx context.Context, reports []Report, responses []Response) error {
	parents := make([]evidence.Parent, 0, len(reports)+len(responses))
	for _, report := range reports {
		parents = append(parents, evidence.ReportParent(report.ID))
	}
	for _, response := range responses {
		parents = append(parents, evidence.ReportResponseParent(response.ID))
	}

	views, err := s.evidence.Views(ctx, parents)
	if err != nil {
		return err
	}
	for i := range reports {
		reports[i].Evidence = views[evidence.ReportParent(reports[i].ID)]
	}
	for i := range responses {
		responses[i].Evidence = views[evidence.ReportResponseParent(responses[i].ID)]
	}
	return nil
}
