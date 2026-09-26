package errs

import (
	"net/http"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api"
)

type UnavailableError struct{}

var _ api.ExternalError = (*UnavailableError)(nil)

func (*UnavailableError) Error() string      { return "authentication service unavailable" }
func (*UnavailableError) ErrorTrace() string { return "authentication dependency unavailable" }
func (*UnavailableError) Code() int          { return http.StatusServiceUnavailable }
