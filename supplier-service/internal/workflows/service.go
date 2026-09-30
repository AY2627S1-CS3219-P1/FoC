package workflows

import (
	"fmt"
	"time"
)

type Service struct {
	repo  Repository
	clock func() time.Time
}

func New(repo Repository, clock func() time.Time) *Service { return &Service{repo: repo, clock: clock} }

// Internal context is retained for server logs; public transports map only
// sentinel errors and never expose dependency failures to callers.
func DependencyError(operation string, err error) error { return fmt.Errorf("%s: %w", operation, err) }
