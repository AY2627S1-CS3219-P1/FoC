package jobs

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/database"
	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/database/moderationdb"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// usersReviewSubmittedTopic is the ingest topic this service consumes. It is
// the exported jobs.UsersReviewSubmittedTopic; kept as a local reference so the
// ingest logic names the topic it validates against.
const usersReviewSubmittedTopic = UsersReviewSubmittedTopic

// maxQuarantineSummary bounds the stored preview of a quarantined payload.
const maxQuarantineSummary = 500

// IngestionInput is a validated review submission ready for durable ingestion.
type IngestionInput struct {
	SourceEventID       uuid.UUID
	ModerationRequestID uuid.UUID
	ReviewID            uuid.UUID
	ReviewerID          uuid.UUID
	ContentRevision     int32
	Comment             string
}

// MalformedInput is a decoded event that cannot become a job (bad identifiers).
type MalformedInput struct {
	Topic      string
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

// Ingest deduplicates on moderation_request_id and, on first sight, inserts the
// durable job. Conflicting or malformed events are quarantined and acknowledged
// (not retried); the event is never reprocessed.
func (s *jobStore) Ingest(ctx context.Context, in IngestionInput) (ingestionResult, error) {
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
			if isUniqueViolation(err) {
				// A concurrent ingestion of the same event committed first.
				result = IngestionDeduplicated
				return nil
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

// QuarantineMalformed records a decoded-but-unprocessable event and acknowledges
// it so the partition can advance.
func (s *jobStore) QuarantineMalformed(ctx context.Context, in MalformedInput) error {
	return database.Within(ctx, s.pool, func(q *moderationdb.Queries) error {
		return s.quarantine(ctx, q, in.MessageKey, in.EventID, in.Reason, in.Comment)
	})
}

func (s *jobStore) quarantine(
	ctx context.Context,
	q *moderationdb.Queries,
	messageKey, eventID, reason, comment string,
) error {
	return q.Quarantine(ctx, moderationdb.QuarantineParams{
		Topic:          usersReviewSubmittedTopic,
		MessageKey:     truncate(messageKey, maxQuarantineSummary),
		EventID:        strPtr(truncate(eventID, maxQuarantineSummary)),
		Reason:         reason,
		PayloadSummary: strPtr(truncate(comment, maxQuarantineSummary)),
	})
}

func jobIdentifiesSame(row moderationdb.ModerationJob, in IngestionInput) bool {
	return row.ReviewID == in.ReviewID &&
		row.ReviewerID == in.ReviewerID &&
		row.ContentRevision == int32(in.ContentRevision) &&
		row.SourceEventID == in.SourceEventID
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// truncate bounds s to n bytes without splitting a UTF-8 rune: a split rune
// makes the stored text invalid UTF-8, which PostgreSQL rejects, failing the
// whole write (an attempt or quarantine row) permanently.
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

// strPtr returns a pointer to s so nullable *string query fields can be bound
// from plain strings (a quoted, possibly-empty summary, not NULL).
func strPtr(s string) *string { return &s }

// maskDiag neutralizes line breaks so a stored diagnostic stays one line.
func maskDiag(s string) string {
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	return truncate(s, maxQuarantineSummary)
}
