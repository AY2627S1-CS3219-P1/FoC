package order

import (
	"context"
	"io"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/images"
)

// MaxStagedProofs is an operational resource limit, separate from the five
// accepted delivery photos. Only uploading identities consume active slots.
// Retired identities remain fenced and cleanup-eligible; they are not revived.
const MaxStagedProofs = 20

// MaxNewProofIdentitiesPerHour bounds abandoned/unknown identity storage per order/courier.
const MaxNewProofIdentitiesPerHour = 200

type ProofState string

const (
	ProofUploading ProofState = "uploading"
	ProofAccepted  ProofState = "accepted"
	ProofDeleting  ProofState = "deleting"
)

// ProofUpload retains immutable identity and signing metadata, not bearer URLs.
// A generation refreshes on expiry or signing-credential rotation.
type ProofUpload struct {
	State                            ProofState
	ID, OrderID, CourierID, UploadID string
	Key, ContentType                 string
	SigningEpoch                     string
	UploadScope                      string
	VerifiedAt                       *time.Time
	SizeBytes                        int64
	Generation                       int64
	GrantedAt, ExpiresAt, CreatedAt  time.Time
}

type ProofUploadInput struct {
	OrderID, UploadID, ContentType string
	SizeBytes                      int64
}

type ProofConfirmation struct {
	OrderID, CourierID string
	ProofIDs           []string
	Unattended         bool
	OutageReason       string
	Outage             *ProofOutage
	ConfirmedAt        time.Time
}

type ConfirmDeliveryInput struct {
	OrderID    string
	ProofIDs   []string
	Unattended bool
	// This requests the exception, not authority to grant it. The server must
	// independently confirm an upload outage and supply its own reason.
	WithoutProof bool
}

type ProofView struct {
	ID, DownloadURL, ContentType string
	ExpiresAt                    time.Time
}

type DeliveryProofView struct {
	Pending      *DeliveryReceipt
	Proofs       []ProofView
	OutageReason string
}

// ProofTx must be backed by the SAME transaction as Tx. Lock the order before
// reading these relationships in separate statements. SaveConfirmation attaches
// exactly these proofs and persists the accepted input atomically with Apply.
// Its adapter must reject cross-order/courier proofs, duplicate confirmations,
// changed proof metadata and rows already fenced for cleanup.
type ProofTx interface {
	Tx
	// Call only after locking the parent; receipt eligibility and timeout share this lock.
	HasPendingDelivery(context.Context, string) (bool, error)
	ProofServerTime(context.Context) (time.Time, error)
	FindProofUpload(context.Context, string, string, string) (ProofUpload, error)
	CountStagedProofs(context.Context, string, string) (int64, error)
	GetProofUpload(context.Context, string) (ProofUpload, error)
	SaveProofUpload(context.Context, ProofUpload) error
	// Preserve the first successful server verification instant across retries.
	MarkProofVerified(context.Context, string) error
	// Atomically fence this scoped identity, including when no upload row exists
	// yet. Exclude it from CountStagedProofs, never retire an accepted proof, and
	// retain the identity fence while the order can admit uploads. Delete bytes
	// only after outstanding grants expire; abandoned grants are not revoked.
	// Fence keys are (order, courier, client upload ID), not server proof IDs.
	// Unknown fences must use separate storage or independent server row keys.
	// A client upload ID equal to a proof ID must never overwrite that proof.
	AbandonProofUpload(context.Context, string, string, string, time.Time) error
	GetProofConfirmation(context.Context, string) (ProofConfirmation, error)
	SaveProofConfirmation(context.Context, ProofConfirmation, []ProofUpload) error
	// This is a LOCAL authoritative read under the order lock, not a remote probe.
	// It must reject superseded/recovered evidence and a mismatched upload scope.
	// Hold a shared lock on the authoritative outage generation through commit,
	// serializing confirmation with recovery publication of that record.
	IsProofOutageCurrent(context.Context, ProofOutage) (bool, error)
}

type ProofRepository interface {
	WithinProofs(context.Context, func(ProofTx) error) error
}

// ProofGrantIssuer reuses the existing immutable S3 signing infrastructure.
// Implementations must return identical grants for a persisted generation, even
// across process restarts, and expire any signing identity when its credentials
// rotate. No concrete retry-stable signing adapter is wired until that contract
// has an acknowledged owner.
type ProofGrantIssuer interface {
	ProofSigningEpoch(context.Context) (string, time.Time, error)
	CreateProofGrant(context.Context, ProofUpload) (images.UploadGrant, error)
}

// ProofObjects keeps storage and decoding outside order transactions. ReadProof
// enforces the bound while reading, not after allocating an unbounded body.
type ProofObjects interface {
	Inspect(context.Context, string) (images.ObjectInfo, error)
	ReadProof(context.Context, string, int64) (io.ReadCloser, error)
	CreateDownload(context.Context, string) (images.DownloadGrant, error)
}

// ProofImageValidator must fully decode the declared format with byte, pixel,
// memory and execution bounds. Merely sniffing a header is insufficient.
type ProofImageValidator interface {
	Validate(context.Context, string, []byte) error
}

type ProofOutage struct {
	Reason, EvidenceID, UploadScope string
	Generation                      int64
	ObservedAt, ValidUntil          time.Time
}

// ProofOutageAuthority is server-controlled. Missing objects, invalid input,
// local signing errors, credentials/configuration failures and a single failed
// client upload never establish an upload-service outage. An absent authority
// fails closed; callers cannot provide an outage reason or observation.
type ProofOutageAuthority interface {
	ConfirmProofOutage(context.Context) (ProofOutage, error)
}

type ProofOptions struct {
	// Stable server-controlled upload-path/bucket identity, never client input.
	UploadScope string
	OutageScope string
	Now         func() time.Time
	NewID       func() string
	Outages     ProofOutageAuthority
}
