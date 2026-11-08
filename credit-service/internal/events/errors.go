package events

import "errors"

// Malformed users.user_deleted facts, acknowledged without recording.
var (
	errInvalidUserID  = errors.New("invalid user ID")
	errInvalidEventID = errors.New("invalid event ID")
)
