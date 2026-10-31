package order

import (
	"context"
	"errors"
	"testing"

	"github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/images"
)

type probeSequence struct {
	result          images.ProofProbeResult
	err             error
	cleanupErr      error
	calls, cleanups int
}

func (s *probeSequence) ProbeProofUpload(context.Context, string) (images.ProofProbeResult, error) {
	s.calls++
	return s.result, s.err
}
func (s *probeSequence) Delete(context.Context, string) error { s.cleanups++; return s.cleanupErr }

type outageMemory struct{ active bool }

func (m *outageMemory) CurrentProofOutage(context.Context, string) (ProofOutage, bool, error) {
	return ProofOutage{}, m.active, nil
}
func (m *outageMemory) PublishProofOutage(_ context.Context, _ string, active bool) error {
	m.active = active
	return nil
}

// Ordinary network/auth/configuration failures do not establish an outage.
// Repeated explicit PUT-service failures do, and recovery revokes publication.
func TestProofOutageCertificationAndRevocation(t *testing.T) {
	repository := &outageMemory{}
	probe := &probeSequence{err: errors.New("not certified")}
	monitor := NewProofOutages(repository, probe, "upload-route")
	_ = monitor.ProbeOnce(t.Context())
	if repository.active {
		t.Fatal("ordinary failure became an outage")
	}
	probe.err = nil
	probe.result.Unavailable = true
	if err := monitor.ProbeOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	if repository.active {
		t.Fatal("one observation became a confirmed outage")
	}
	if err := monitor.ProbeOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !repository.active {
		t.Fatal("repeated service failures not published")
	}
	probe.result.Unavailable = false
	if err := monitor.ProbeOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	if repository.active {
		t.Fatal("recovery failed to revoke outage")
	}
}

func TestProofProbeCleanupFailureCannotCreateMoreObjects(t *testing.T) {
	probe := &probeSequence{
		result:     images.ProofProbeResult{CleanupKey: "fixed-probe"},
		err:        errors.New("cleanup pending"),
		cleanupErr: errors.New("delete refused"),
	}
	monitor := NewProofOutages(&outageMemory{}, probe, "route")
	_ = monitor.ProbeOnce(t.Context())
	for range 5 {
		_ = monitor.ProbeOnce(t.Context())
	}
	if probe.calls != 1 || probe.cleanups != 5 {
		t.Fatalf("probe accumulated objects: %d writes %d cleanup retries", probe.calls, probe.cleanups)
	}
}
