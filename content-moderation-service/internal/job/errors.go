package job

import "errors"

// ErrClaimLost cancels inference; only the current owner may finalize.
var ErrClaimLost = errors.New("moderation job claim was lost")
