package job

import "errors"

// ErrClaimLost is set when a worker's heartbeat finds its claim gone: another
// worker reclaimed the job after a lease expiry, or the claim was fenced away.
// The in-flight assessment is discarded and the job is left for the winner.
var ErrClaimLost = errors.New("moderation job claim was lost")
