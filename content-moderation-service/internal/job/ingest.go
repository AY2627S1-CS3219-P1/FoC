package job

import (
	"context"
	"errors"
	"unicode/utf8"

	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/database"
	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/database/moderationdb"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const maxQuarantineSummary = 500

type IngestionInput struct {
	SourceEventID       uuid.UUID
	ModerationRequestID uuid.UUID
	ReviewID            uuid.UUID
	ReviewerID          uuid.UUID
	ContentRevision     int32
	Comment             string
}

type MalformedInput struct {
	MessageKey string
	EventID    string
	Reason     string
	Comment    string
}

type ingestionResult int

const (
	IngestionInserted ingestionResult = iota
	IngestionDeduplicated
	IngestionConflictQuarantined
	IngestionMalformedQuarantined
)

func (r ingestionResult) String() string {
	switch r {
	case IngestionInserted:
		return "ingested"
	case IngestionDeduplicated:
		return "deduplicated"
	case IngestionConflictQuarantined:
		return "conflict_quarantined"
	case IngestionMalformedQuarantined:
		return "malformed_quarantined"
	default:
		return "unknown"
	}
}

// Ingest deduplicates by moderation_request_id; conflicting snapshots are quarantined.
func (s *Service) Ingest(ctx context.Context, in IngestionInput) (ingestionResult, error) {
	var result ingestionResult
	err := database.Within(ctx, s.pool, func(q *moderationdb.Queries) error {
		existing, err := q.GetJobByModerationRequest(ctx, in.ModerationRequestID)
		if !errors.Is(err, pgx.ErrNoRows) && err != nil {
			return err
		}
		if existing.ID != uuid.Nil {
			if jobIdentifiesSame(existing, in) {
				result = IngestionDeduplicated
				return nil
			}
			if err := s.quarantine(ctx, q, in.ReviewID.String(), in.SourceEventID.String(),
				"conflicting identity for moderation_request_id", in.Comment); err != nil {
				return err
			}
			result = IngestionConflictQuarantined
			return nil
		}

		if _, err := q.InsertJob(ctx, moderationdb.InsertJobParams{
			ModerationRequestID: in.ModerationRequestID,
			SourceEventID:       in.SourceEventID,
			ReviewID:            in.ReviewID,
			ReviewerID:          in.ReviewerID,
			ContentRevision:     in.ContentRevision,
			Comment:             in.Comment,
			RunGeneration:       1,
			Status:              "queued",
			AttemptCount:        0,
		}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				// ON CONFLICT avoids aborting the transaction. Re-read after
				// the competing insert commits and verify its immutable snapshot.
				existing, readErr := q.GetJobByModerationRequest(ctx, in.ModerationRequestID)
				if readErr == nil && jobIdentifiesSame(existing, in) {
					result = IngestionDeduplicated
					return nil
				}
				if readErr != nil && !errors.Is(readErr, pgx.ErrNoRows) {
					return readErr
				}
				result = IngestionConflictQuarantined
				return s.quarantine(ctx, q, in.ReviewID.String(), in.SourceEventID.String(),
					"conflicting submission identity", in.Comment)
			}
			return err
		}
		result = IngestionInserted
		return nil
	})
	if err != nil {
		return IngestionInserted, err
	}
	return result, nil
}

func (s *Service) QuarantineMalformed(ctx context.Context, in MalformedInput) error {
	return database.Within(ctx, s.pool, func(q *moderationdb.Queries) error {
		return s.quarantine(ctx, q, in.MessageKey, in.EventID, in.Reason, in.Comment)
	})
}

func (s *Service) quarantine(
	ctx context.Context,
	q *moderationdb.Queries,
	messageKey, eventID, reason, comment string,
) error {
	return q.Quarantine(ctx, moderationdb.QuarantineParams{
		Topic:          UsersReviewSubmittedTopic,
		MessageKey:     truncate(messageKey, maxQuarantineSummary),
		EventID:        strPtr(truncate(eventID, maxQuarantineSummary)),
		Reason:         reason,
		PayloadSummary: strPtr(truncate(comment, maxQuarantineSummary)),
	})
}

func jobIdentifiesSame(row moderationdb.ModerationJob, in IngestionInput) bool {
	return row.ReviewID == in.ReviewID &&
		row.ReviewerID == in.ReviewerID &&
		row.ContentRevision == in.ContentRevision &&
		row.Comment == in.Comment &&
		row.SourceEventID == in.SourceEventID
}

// Avoid splitting UTF-8 runes: PostgreSQL rejects invalid text.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := s[:n]
	for len(cut) > 0 && !utf8.ValidString(cut) {
		cut = cut[:len(cut)-1]
	}
	return cut
}

func strPtr(s string) *string { return &s }
