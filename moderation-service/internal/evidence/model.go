// Package evidence stores image evidence for reports, appeals and their
// responses (RP1.6, RP2.2, RP8.2, RP9.4). It follows review photos: the browser
// uploads straight to object storage through a signed grant, the staged row is
// checked against the stored object outside the submission transaction, and
// attached inside it. Callers own visibility; this package only signs URLs.
package evidence

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// MaxPerParent caps the images on one report, appeal or response.
const MaxPerParent = 5

// ParentKind names what an image is attached to.
type ParentKind int

const (
	ParentReport ParentKind = iota + 1
	ParentReportResponse
	ParentAppeal
	ParentAppealResponse
)

// Parent is the one row an attached image belongs to.
type Parent struct {
	Kind ParentKind
	ID   uuid.UUID
}

func ReportParent(id uuid.UUID) Parent         { return Parent{Kind: ParentReport, ID: id} }
func ReportResponseParent(id uuid.UUID) Parent { return Parent{Kind: ParentReportResponse, ID: id} }
func AppealParent(id uuid.UUID) Parent         { return Parent{Kind: ParentAppeal, ID: id} }
func AppealResponseParent(id uuid.UUID) Parent { return Parent{Kind: ParentAppealResponse, ID: id} }

// Prepared is a staged image whose stored object matched its row. Attaching it
// compares the same fields again, so a replaced row is not attached.
type Prepared struct {
	ID          uuid.UUID
	ObjectKey   string
	ContentType string
	SizeBytes   int64
}

// View is an attached image with a short-lived download URL.
type View struct {
	ID          uuid.UUID
	URL         string
	ContentType string
	ExpiresAt   time.Time
}

// Owner is the report or appeal whose visibility rules govern an image, also
// for images attached to a response. Exactly one ID is set.
type Owner struct {
	ReportID uuid.UUID
	AppealID uuid.UUID
}

// Persistence rows

type StagedInput struct {
	ID              uuid.UUID
	OwnerID         uuid.UUID
	ObjectKey       string
	ContentType     string
	SizeBytes       int64
	UploadExpiresAt time.Time
}

type Staged struct {
	Prepared
	State string
}

type Attached struct {
	ID          uuid.UUID
	ObjectKey   string
	ContentType string
	Parent      Parent
}

type Located struct {
	Owner
	ObjectKey   string
	ContentType string
}

type Orphan struct {
	ID        uuid.UUID
	ObjectKey string
}

func newID() (uuid.UUID, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return uuid.Nil, fmt.Errorf("generate uuid: %w", err)
	}
	return id, nil
}

var (
	errEvidenceNotFound   = errors.New("evidence not found")
	errStorageNotEnabled  = errors.New("evidence storage is not configured")
	errEvidenceNotReady   = errors.New("evidence is not ready to attach")
	errEvidenceInvalid    = errors.New("evidence metadata is invalid")
	errEvidenceNotStaged  = errors.New("evidence was not staged by this user")
	errUploadUnavailable  = errors.New("evidence uploads are not available")
	errUploadTemporarily  = errors.New("evidence storage is temporarily unavailable")
	errTooMuchEvidence    = errors.New("too many evidence images")
	errDuplicateEvidence  = errors.New("evidence IDs must be unique")
	errEvidenceIDRequired = errors.New("evidence ID is required")
)
