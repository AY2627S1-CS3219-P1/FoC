package disablement

import (
	"fmt"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/idempotency"
)

type Service struct {
	repo   Repository
	clock  func() time.Time
	runner *idempotency.Runner
}

func NewService(repo Repository, clock func() time.Time) *Service {
	return &Service{repo: repo, clock: clock, runner: idempotency.New(clock)}
}

// Internal context is retained for server logs; public transports map only
// sentinel errors and never expose dependency failures to callers.
func DependencyError(operation string, err error) error { return fmt.Errorf("%s: %w", operation, err) }
