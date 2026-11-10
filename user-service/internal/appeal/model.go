// Package appeal implements appeals (RP7-RP12): the user a final decision
// affects appeals it, an administrator may ask another affected participant to
// respond, and the administrator closes the appeal by upholding or overturning
// every challenged decision. Overturned outcomes are applied in the closing
// transaction through the domains that own them. The package follows the shape
// of the report package, whose lifecycle it shares.
package appeal

import (
	"errors"
	"fmt"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/pagination"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/evidence"
	"github.com/google/uuid"
)

// Status values match the user_appeals.status check constraint.
type Status string

const (
	StatusOpen             Status = "open"
	StatusUnderReview      Status = "under_review"
	StatusAwaitingResponse Status = "awaiting_response"
	StatusClosed           Status = "closed"
)

func (s Status) IsClosed() bool { return s == StatusClosed }

// Kind values match the user_appeal_decisions.kind check constraint.
type Kind string

const (
	// KindReportOutcome challenges the outcome of a closed report.
	KindReportOutcome Kind = "report_outcome"
	// KindWarning challenges an account warning no report produced.
	KindWarning Kind = "warning"
	// KindReviewRemoval challenges an administrator's removal of the
	// appellant's review outside a report.
	KindReviewRemoval Kind = "review_removal"
	// KindReviewRetention challenges a closed report's decision to keep a
	// review about the appellant.
	KindReviewRetention Kind = "review_retention"
	// KindCreditAdjustment is reserved until Credit records administrative
	// adjustments (#302).
	KindCreditAdjustment Kind = "credit_adjustment"
)

// Outcome values match the user_appeal_decisions.outcome check constraint.
type Outcome string

const (
	OutcomeUpheld     Outcome = "upheld"
	OutcomeOverturned Outcome = "overturned"
)

func (o Outcome) IsValid() bool { return o == OutcomeUpheld || o == OutcomeOverturned }

// ReportOutcome values mirror user_reports.outcome, read when an overturned
// report outcome is undone.
type ReportOutcome string

const (
	ReportOutcomeNoAction      ReportOutcome = "no_action"
	ReportOutcomeWarning       ReportOutcome = "warning"
	ReportOutcomeSuspension    ReportOutcome = "suspension"
	ReportOutcomeReviewRemoval ReportOutcome = "review_removal"
)

// Target identifies one decision. TargetID is the report, warning, review or
// credit entry the kind refers to.
type Target struct {
	Kind     Kind
	TargetID uuid.UUID
}

type Submission struct {
	IdempotencyKey uuid.UUID
	// RequestID is uuid.Nil when the client names no originating order.
	RequestID uuid.UUID
	Reason    string
	Targets   []Target
	// EvidenceIDs are staged images to attach, in order (RP8.2).
	EvidenceIDs []uuid.UUID
}

// Decision is a challenged decision as stored. Outcome is empty until the
// appeal closes; CounterpartID is uuid.Nil when nobody else is affected.
type Decision struct {
	ID              uuid.UUID
	AppealID        uuid.UUID
	Target          Target
	CounterpartID   uuid.UUID
	CounterpartName string
	Outcome         Outcome
	OutcomeReason   string
}

// Appeal is a stored appeal with its decisions and joined display names.
type Appeal struct {
	ID            uuid.UUID
	AppellantID   uuid.UUID
	AppellantName string
	RequestID     uuid.UUID
	Reason        string
	Status        Status
	AwaitedUserID uuid.UUID
	Decisions     []Decision
	ClosedAt      time.Time
	ClosedBy      uuid.UUID
	CloserName    string
	CreatedAt     time.Time
	UpdatedAt     time.Time
	Evidence      []evidence.View
}

// Involves reports whether userID filed the appeal or is a counterpart of one
// of its decisions: the non-administrators who may read it (RP11.3).
func (a Appeal) Involves(userID uuid.UUID) bool {
	if a.AppellantID == userID {
		return true
	}
	for _, decision := range a.Decisions {
		if decision.CounterpartID == userID {
			return true
		}
	}
	return false
}

// Response is a participant's reply to a requested response.
type Response struct {
	ID            uuid.UUID
	AppealID      uuid.UUID
	ResponderID   uuid.UUID
	ResponderName string
	Body          string
	CreatedAt     time.Time
	Evidence      []evidence.View
}

// Detail is an appeal with its responses, oldest first.
type Detail struct {
	Appeal    Appeal
	Responses []Response
}

type Page struct {
	Appeals []Appeal
	pagination.PageInfo
}

// Filter narrows an administrator's appeal list. Zero values match every row.
type Filter struct {
	Status      Status
	AppellantID uuid.UUID
}

// Resolution is the administrator's outcome for one decision.
type Resolution struct {
	DecisionID uuid.UUID
	Outcome    Outcome
	Reason     string
}

// Persistence inputs

type AppealInput struct {
	ID             uuid.UUID
	AppellantID    uuid.UUID
	RequestID      uuid.UUID
	Reason         string
	IdempotencyKey uuid.UUID
	RequestHash    string
}

type DecisionInput struct {
	ID            uuid.UUID
	AppealID      uuid.UUID
	Target        Target
	CounterpartID uuid.UUID
}

type ResponseInput struct {
	ID          uuid.UUID
	AppealID    uuid.UUID
	ResponderID uuid.UUID
	Body        string
}

// ExistingAppeal is the appeal a previous submission with the same key created.
type ExistingAppeal struct {
	ID          uuid.UUID
	RequestHash string
}

// Eligibility facts

type ReportFacts struct {
	ReporterID uuid.UUID
	SubjectID  uuid.UUID
	ReviewID   uuid.UUID
	Status     string
	Outcome    ReportOutcome
	RequestID  uuid.UUID
}

type WarningFacts struct {
	UserID    uuid.UUID
	RequestID uuid.UUID
	ReportID  uuid.UUID
	Active    bool
}

type ReviewFacts struct {
	ReviewerID           uuid.UUID
	RevieweeID           uuid.UUID
	OrderID              uuid.UUID
	Removed              bool
	HasUnrestoredRemoval bool
	RemovedByReport      bool
	RetainedByReport     bool
}

// LockedAppeal is the state a transition checks while holding the row lock.
type LockedAppeal struct {
	AppellantID   uuid.UUID
	Status        Status
	AwaitedUserID uuid.UUID
}

// LockedDecision carries what closing needs to apply an overturned outcome.
// The report fields are set for KindReportOutcome; ReportWarningID is uuid.Nil
// when the report recorded no warning.
type LockedDecision struct {
	ID              uuid.UUID
	Target          Target
	CounterpartID   uuid.UUID
	ReportOutcome   ReportOutcome
	ReportSubjectID uuid.UUID
	ReportReviewID  uuid.UUID
	ReportWarningID uuid.UUID
}

// newID generates identifiers in Go, as the report package does.
func newID() (uuid.UUID, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return uuid.Nil, fmt.Errorf("generate uuid: %w", err)
	}
	return id, nil
}

var (
	errAppealNotFound         = errors.New("appeal not found")
	errTargetNotFound         = errors.New("appealed decision not found")
	errDecisionAlreadyAppeal  = errors.New("decision is already under an unclosed appeal")
	errAppealInvalid          = errors.New("appeal data is invalid")
	errAppealStateChanged     = errors.New("appeal state changed")
	errIdempotencyKeyConflict = errors.New("idempotency key was used for a different appeal")
)
