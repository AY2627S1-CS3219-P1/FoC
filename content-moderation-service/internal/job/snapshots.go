package job

import "github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/database/moderationdb"

func jobSnapshot(row moderationdb.ModerationJob) JobSnapshot {
	return JobSnapshot{
		ID:                  row.ID,
		ModerationRequestID: row.ModerationRequestID,
		SourceEventID:       row.SourceEventID,
		ReviewID:            row.ReviewID,
		ReviewerID:          row.ReviewerID,
		ContentRevision:     row.ContentRevision,
		Comment:             row.Comment,
		PolicyVersion:       row.PolicyVersion,
		RunGeneration:       row.RunGeneration,
	}
}
