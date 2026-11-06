package warning

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api/errs"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/pagination"
	"github.com/google/uuid"
)

var ErrInvalidRequest = errors.New("invalid courier warning request")

type Service struct {
	repository Repository
}

func NewService(repository Repository) *Service {
	return &Service{repository: repository}
}

func (s *Service) RecordCourierWarning(ctx context.Context, request CourierWarningRequest) error {
	eventID, err := parseID(request.EventID)
	if err != nil {
		return fmt.Errorf("%w: invalid event ID", ErrInvalidRequest)
	}
	courierID, err := parseID(request.CourierID)
	if err != nil {
		return fmt.Errorf("%w: invalid courier ID", ErrInvalidRequest)
	}
	requestID, err := parseID(request.RequestID)
	if err != nil {
		return fmt.Errorf("%w: invalid request ID", ErrInvalidRequest)
	}
	reason := strings.TrimSpace(request.Reason)
	if reason == "" || utf8.RuneCountInString(reason) > 2000 {
		return fmt.Errorf("%w: invalid reason", ErrInvalidRequest)
	}
	if request.OccurredAt.IsZero() {
		return fmt.Errorf("%w: missing occurrence time", ErrInvalidRequest)
	}

	eventIDCopy := eventID
	_, err = s.repository.Create(ctx, Warning{
		UserID: courierID, RequestID: requestID, Reason: reason,
		Status: StatusActive, SourceEventID: &eventIDCopy,
		CreatedAt: request.OccurredAt.UTC(),
	})
	if err != nil {
		return fmt.Errorf("record courier warning: %w", err)
	}
	return nil
}

func (s *Service) ListWarnings(
	ctx context.Context,
	userID uuid.UUID,
	page pagination.Pagination,
) (WarningPage, error) {
	if userID == uuid.Nil {
		return WarningPage{}, fmt.Errorf("%w: invalid user ID", ErrInvalidRequest)
	}
	warnings, err := s.repository.ListByUser(ctx, userID, page.Offset(), page.PageSize())
	if err != nil {
		return WarningPage{}, fmt.Errorf("list warnings: %w", err)
	}
	total, err := s.repository.CountByUser(ctx, userID)
	if err != nil {
		return WarningPage{}, fmt.Errorf("count warnings: %w", err)
	}
	return WarningPage{
		Warnings: warnings,
		PageInfo: pagination.NewPageInfo(page.Page(), page.PageSize(), total),
	}, nil
}

func (s *Service) MarkWarningAsRemoved(
	ctx context.Context,
	warningID uuid.UUID,
	removedReason *string,
	appealID *uuid.UUID,
	actorID uuid.UUID,
) error {
	if warningID == uuid.Nil || actorID == uuid.Nil || (appealID != nil && *appealID == uuid.Nil) {
		return errs.NewBadRequestError("invalid warning removal")
	}
	if removedReason != nil && utf8.RuneCountInString(*removedReason) > 2000 {
		return errs.NewBadRequestError("removal reason must not exceed 2000 characters")
	}

	_, err := s.repository.Remove(ctx, warningID, time.Now().UTC(), removedReason, appealID, &actorID)
	if errors.Is(err, ErrNotFound) {
		return errs.WrapNotFoundError(ErrNotFound, "warning not found")
	}
	if err != nil {
		return fmt.Errorf("mark warning as removed: %w", err)
	}
	return nil
}

func parseID(value string) (uuid.UUID, error) {
	id, err := uuid.Parse(value)
	if err != nil || id == uuid.Nil {
		return uuid.Nil, errors.New("invalid UUID")
	}
	return id, nil
}
