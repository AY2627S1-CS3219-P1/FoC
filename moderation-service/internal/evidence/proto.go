package evidence

import (
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api/errs"
	evidencev1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/moderation/evidence/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/images"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Conversions shared by the report and appeal Connect handlers.

// ParseIDs parses the evidence IDs of a submission request.
func ParseIDs(values []string) ([]uuid.UUID, error) {
	if len(values) == 0 {
		return nil, nil
	}
	ids := make([]uuid.UUID, len(values))
	for i, value := range values {
		id, err := uuid.Parse(value)
		if err != nil || id == uuid.Nil {
			return nil, errs.NewBadRequestError("invalid evidence ID")
		}
		ids[i] = id
	}
	return ids, nil
}

func ToProtos(views []View) []*evidencev1.Evidence {
	if len(views) == 0 {
		return nil
	}
	out := make([]*evidencev1.Evidence, len(views))
	for i, view := range views {
		out[i] = ToProto(view)
	}
	return out
}

func ToProto(view View) *evidencev1.Evidence {
	return &evidencev1.Evidence{
		Id:          view.ID.String(),
		Url:         view.URL,
		ContentType: view.ContentType,
		ExpiresAt:   timestamppb.New(view.ExpiresAt),
	}
}

func ToGrantProto(id uuid.UUID, grant images.UploadGrant) *evidencev1.EvidenceUploadGrant {
	return &evidencev1.EvidenceUploadGrant{
		EvidenceId:    id.String(),
		UploadUrl:     grant.URL,
		UploadHeaders: grant.Headers,
		ExpiresAt:     timestamppb.New(grant.ExpiresAt),
	}
}
