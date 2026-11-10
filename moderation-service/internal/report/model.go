// Package report implements user reports (RP1-RP5): a user reports another user
// about one review or one order, the reported participant may respond, and an
// administrator reviews the report and closes it with a reasoned outcome. The
// domain model, repository interface, service logic, sqlc repository and
// Connect handler live here, following the review package.
package report

import (
	"errors"
	"fmt"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/moderation-service/internal/evidence"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/pagination"
	"github.com/google/uuid"
)

// Category values match the user_reports.category check constraint.
type Category string

const (
	CategoryHarassment           Category = "harassment"
	CategoryFraud                Category = "fraud"
	CategoryNoShow               Category = "no_show"
	CategoryInappropriateContent Category = "inappropriate_content"
	CategoryOther                Category = "other"
)

func (c Category) IsValid() bool {
	switch c {
	case CategoryHarassment, CategoryFraud, CategoryNoShow, CategoryInappropriateContent, CategoryOther:
		return true
	default:
		return false
	}
}

// Status values match the user_reports.status check constraint.
type Status string

const (
	StatusOpen             Status = "open"
	StatusUnderReview      Status = "under_review"
	StatusAwaitingResponse Status = "awaiting_response"
	StatusClosed           Status = "closed"
)

func (s Status) IsClosed() bool { return s == StatusClosed }

// Outcome values match the user_reports.outcome check constraint.
type Outcome string

const (
	OutcomeNoAction      Outcome = "no_action"
	OutcomeWarning       Outcome = "warning"
	OutcomeSuspension    Outcome = "suspension"
	OutcomeReviewRemoval Outcome = "review_removal"
)

func (o Outcome) IsValid() bool {
	switch o {
	case OutcomeNoAction, OutcomeWarning, OutcomeSuspension, OutcomeReviewRemoval:
		return true
	default:
		return false
	}
}

// Target is exactly one review or one order. For an order, SubjectID names the
// participant being reported; for a review, the subject is the review's author.
type Target struct {
	ReviewID  uuid.UUID
	OrderID   uuid.UUID
	SubjectID uuid.UUID
}

func ReviewTarget(reviewID uuid.UUID) Target { return Target{ReviewID: reviewID} }

func OrderTarget(orderID, subjectID uuid.UUID) Target {
	return Target{OrderID: orderID, SubjectID: subjectID}
}

func (t Target) IsReview() bool { return t.ReviewID != uuid.Nil }

func (t Target) IsValid() bool {
	if t.IsReview() {
		return t.OrderID == uuid.Nil && t.SubjectID == uuid.Nil
	}
	return t.OrderID != uuid.Nil && t.SubjectID != uuid.Nil
}

type Submission struct {
	Target      Target
	Category    Category
	Description string
	// EvidenceIDs are staged images to attach, in order (RP1.6).
	EvidenceIDs []uuid.UUID
}

// Report is a stored report with display names joined at read time. ReviewID
// or OrderID is uuid.Nil when the report targets the other kind. The outcome
// fields are zero until the report is closed.
type Report struct {
	ID            uuid.UUID
	ReporterID    uuid.UUID
	ReporterName  string
	SubjectID     uuid.UUID
	SubjectName   string
	ReviewID      uuid.UUID
	OrderID       uuid.UUID
	Category      Category
	Description   string
	Status        Status
	Outcome       Outcome
	OutcomeReason string
	ClosedAt      time.Time
	ClosedBy      uuid.UUID
	CloserName    string
	CreatedAt     time.Time
	UpdatedAt     time.Time
	Evidence      []evidence.View
}

// Involves reports whether userID is the reporter or the reported participant,
// the two non-administrators who may read the report.
func (r Report) Involves(userID uuid.UUID) bool {
	return r.ReporterID == userID || r.SubjectID == userID
}

// Response is the reported participant's reply to a report.
type Response struct {
	ID            uuid.UUID
	ReportID      uuid.UUID
	ResponderID   uuid.UUID
	ResponderName string
	Body          string
	CreatedAt     time.Time
	Evidence      []evidence.View
}

// Detail is a report with its responses, oldest first.
type Detail struct {
	Report    Report
	Responses []Response
}

type ReportInput struct {
	ID         uuid.UUID
	ReporterID uuid.UUID
	SubjectID  uuid.UUID
	ReviewID   uuid.UUID
	OrderID    uuid.UUID
	// RequestID is the originating order: the order target, or the reported
	// review's order.
	RequestID   uuid.UUID
	Category    Category
	Description string
}

type ResponseInput struct {
	ID          uuid.UUID
	ReportID    uuid.UUID
	ResponderID uuid.UUID
	Body        string
}

// Filter narrows an administrator's report list. Zero values match every row.
type Filter struct {
	Status    Status
	SubjectID uuid.UUID
	ReviewID  uuid.UUID
	OrderID   uuid.UUID
}

type Closure struct {
	Outcome  Outcome
	Reason   string
	ClosedBy uuid.UUID
}

// LockedReport is the state a transition checks while holding the row lock.
// RequestID is the originating order: the order target, or the review's order.
type LockedReport struct {
	ReporterID uuid.UUID
	SubjectID  uuid.UUID
	ReviewID   uuid.UUID
	Status     Status
	RequestID  uuid.UUID
}

func (l LockedReport) Involves(userID uuid.UUID) bool {
	return l.ReporterID == userID || l.SubjectID == userID
}

type Page struct {
	Reports []Report
	pagination.PageInfo
}

// Consequences

// Warning asks for an account warning against the reported participant.
type Warning struct {
	UserID    uuid.UUID
	RequestID uuid.UUID
	ReportID  uuid.UUID
	ActorID   uuid.UUID
	Reason    string
}

// Suspension asks for the reported participant's account to be suspended.
type Suspension struct {
	UserID   uuid.UUID
	ReportID uuid.UUID
	ActorID  uuid.UUID
	Reason   string
}

// ReviewRemoval removes the reported review as the report's outcome.
type ReviewRemoval struct {
	ReviewID uuid.UUID
	ReportID uuid.UUID
	ActorID  uuid.UUID
	Reason   string
}

// newID generates identifiers in Go, as the review package does.
func newID() (uuid.UUID, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return uuid.Nil, fmt.Errorf("generate uuid: %w", err)
	}
	return id, nil
}

var (
	errReportNotFound     = errors.New("report not found")
	errReviewNotFound     = errors.New("review not found")
	errReportAlreadyOpen  = errors.New("an unclosed report already exists for this target")
	errReportInvalid      = errors.New("report data is invalid")
	errReportStateChanged = errors.New("report state changed")
)
