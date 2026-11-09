package moderation

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
		ConfigVersionID:     row.ConfigVersionID,
		PolicyVersion:       row.PolicyVersion,
		RunGeneration:       row.RunGeneration,
	}
}

func configurationSnapshot(row moderationdb.ModerationConfigVersion) ConfigurationSnapshot {
	return ConfigurationSnapshot{
		SystemMessage:       row.SystemMessage,
		VersionID:           row.VersionID,
		ModelID:             row.ModelID,
		Parameters:          row.Parameters,
		OutputMode:          row.OutputMode,
		PolicyVersion:       row.PolicyVersion,
		ProviderFingerprint: row.ProviderFingerprint,
	}
}
