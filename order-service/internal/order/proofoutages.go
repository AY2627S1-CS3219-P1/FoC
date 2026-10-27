package order

import (
	"context"
	"errors"
	"log/slog"
	"time"

	db "github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/database/orderdb"
	"github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/images"
	"github.com/google/uuid"
)

type ProofProbe interface {
	ProbeProofUpload(context.Context, string) (images.ProofProbeResult, error)
	Delete(context.Context, string) error
}

type ProofOutageRepository interface {
	CurrentProofOutage(context.Context, string) (ProofOutage, bool, error)
	PublishProofOutage(context.Context, string, bool) error
}

// ProofOutages certifies the upload route only after repeated explicit 5xx PUT
// responses. Its publication is ordered against receipt admission by the shared
// outage row lock. Probing never runs inside an order transaction.
type ProofOutages struct {
	repository ProofOutageRepository
	probe      ProofProbe
	scope      string
	failures   int
	cleanup    string
	key        string
}

func NewProofOutages(repository ProofOutageRepository, probe ProofProbe, scope string) *ProofOutages {
	return &ProofOutages{repository: repository, probe: probe, scope: scope, key: "order/proof/probe/" + uuid.NewString()}
}

func (s *ProofOutages) ConfirmProofOutage(ctx context.Context) (ProofOutage, error) {
	value, active, err := s.repository.CurrentProofOutage(ctx, s.scope)
	if err != nil {
		return ProofOutage{}, err
	}
	if !active {
		return ProofOutage{}, ErrPrecondition
	}
	// Expiry is checked with the database receipt clock, not a replica's clock.
	return value, nil
}

func (s *ProofOutages) ProbeOnce(ctx context.Context) error {
	if s.probe == nil {
		return ErrUnavailable
	}
	if s.cleanup != "" {
		if err := s.probe.Delete(ctx, s.cleanup); err != nil {
			return errors.New("proof probe cleanup remains pending")
		}
		s.cleanup = ""
	}
	result, err := s.probe.ProbeProofUpload(ctx, s.key)
	s.cleanup = result.CleanupKey
	if result.Unavailable && err == nil {
		s.failures++
	} else {
		s.failures = 0
	}
	// Revocation publishes even for configuration/transport uncertainty. Those
	// failures can never extend an older certified outage or grant a new bypass.
	if e := s.repository.PublishProofOutage(ctx, s.scope, s.failures >= 2); e != nil {
		return e
	}
	return err
}

func (s *ProofOutages) Run(ctx context.Context) {
	timer := time.NewTicker(10 * time.Second)
	defer timer.Stop()
	for {
		bounded, cancel := context.WithTimeout(ctx, 8*time.Second)
		err := s.ProbeOnce(bounded)
		cancel()
		if err != nil && ctx.Err() == nil {
			slog.Warn("proof outage certification unavailable", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
	}
}

func (r *PostgresRepository) CurrentProofOutage(ctx context.Context, scope string) (ProofOutage, bool, error) {
	row, err := db.New(r.pool).CurrentProofOutage(ctx, scope)
	if errors.Is(persistenceError(err), ErrNotFound) {
		return ProofOutage{}, false, nil
	}
	if err != nil {
		return ProofOutage{}, false, err
	}
	return ProofOutage{
		EvidenceID:  row.EvidenceID.String(),
		UploadScope: row.Scope,
		Generation:  row.Generation,
		ObservedAt:  row.ObservedAt,
		ValidUntil:  row.ValidUntil,
		Reason:      row.Reason,
	}, row.Active, nil
}

func (r *PostgresRepository) PublishProofOutage(ctx context.Context, scope string, active bool) error {
	q := db.New(r.pool)
	now, err := q.ProofServerTime(ctx)
	if err != nil {
		return err
	}
	return q.PublishProofOutage(ctx, db.PublishProofOutageParams{
		Scope:      scope,
		EvidenceID: uuid.New(),
		ObservedAt: now,
		ValidUntil: now.Add(30 * time.Second),
		Reason:     "storage_service_unavailable",
		Active:     active,
	})
}
