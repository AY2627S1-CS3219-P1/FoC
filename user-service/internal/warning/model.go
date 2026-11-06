package warning

import (
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/pagination"
	"github.com/google/uuid"
)

type Status string

const (
	StatusActive  Status = "active"
	StatusRemoved Status = "removed"
)

type Warning struct {
	ID            uuid.UUID
	UserID        uuid.UUID
	RequestID     uuid.UUID
	ReportID      *uuid.UUID
	Reason        string
	Status        Status
	SourceEventID *uuid.UUID
	RemovedAt     *time.Time
	RemovedReason *string
	AppealID      *uuid.UUID
	CreatedAt     time.Time
	UpdatedAt     time.Time
	DeletedAt     *time.Time
	CreatedBy     *uuid.UUID
	UpdatedBy     *uuid.UUID
}

type WarningPage struct {
	Warnings []Warning
	PageInfo pagination.PageInfo
}

type CourierWarningRequest struct {
	EventID    string
	CourierID  string
	RequestID  string
	Reason     string
	OccurredAt time.Time
}
