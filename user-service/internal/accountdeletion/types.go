// Package accountdeletion implements the background worker that drains a
// permanently-deleting account's outstanding work and then hard-deletes it.
//
// Acceptance sets the irreversible is_deleting flag and inserts one pending-job
// row; the worker claims jobs with a lease, polls the Order and Credit services
// for outstanding work, postpones while any exists, and finalizes by
// hard-deleting the account plus its sessions, tokens and favourites while
// recording a committed user_deleted fact for publication.
package accountdeletion

import "time"

// Config holds the timing knobs for acceptance and the worker. Values are
// validated by NewConfig so a misconfiguration fails fast at startup.
type Config struct {
	// AdmissionGracePeriod is the minimum age of an accepted account before it
	// becomes eligible for finalization. It gives in-flight work time to
	// complete before the account is removed.
	AdmissionsGracePeriod time.Duration
	// InitialDrainDelay is how long after acceptance the first drain attempt is
	// scheduled, so a crashing worker resumes without hammering the ports.
	InitialDrainDelay time.Duration
	// Lease is how long a claimed job or committed fact is exclusive to one
	// replica; a stale replica past its lease is superseded.
	Lease time.Duration
	// PollInterval is how often the worker and relay look for more work when
	// idle.
	PollInterval time.Duration
}

// NewConfig validates and fills defaults, rejecting non-positive grace and lease
// windows that would let the worker finalize before admissions are safe.
func NewConfig(admissionGrace, initialDrain, lease, poll time.Duration) (Config, error) {
	if admissionGrace <= 0 || lease <= 0 || poll <= 0 {
		return Config{}, newConfigError("grace period, lease and poll interval must be positive")
	}
	if initialDrain < 0 {
		return Config{}, newConfigError("initial drain delay must not be negative")
	}
	return Config{
		AdmissionsGracePeriod: admissionGrace,
		InitialDrainDelay:     initialDrain,
		Lease:                 lease,
		PollInterval:          poll,
	}, nil
}

type configError struct{ msg string }

func (e *configError) Error() string { return e.msg }

func newConfigError(msg string) error { return &configError{msg: msg} }
