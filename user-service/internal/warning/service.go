package warning

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

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

func (s *Service) ListWarnings(ctx context.Context, userID uuid.UUID) ([]Warning, error) {
	if userID == uuid.Nil {
		return nil, fmt.Errorf("%w: invalid user ID", ErrInvalidRequest)
	}
	warnings, err := s.repository.ListByUser(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list warnings: %w", err)
	}
	return warnings, nil
}

func parseID(value string) (uuid.UUID, error) {
	id, err := uuid.Parse(value)
	if err != nil || id == uuid.Nil {
		return uuid.Nil, errors.New("invalid UUID")
	}
	return id, nil
}
