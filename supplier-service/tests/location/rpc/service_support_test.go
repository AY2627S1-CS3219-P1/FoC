package location_test

import (
	"context"
	"net/http"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/supplier/location/v1/locationv1connect"
	domain "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/location/shared"
	r "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/rpc/location/additionrequest"
	d "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/rpc/location/disablement"
)

type testOperations interface {
	d.Operations
	r.Operations
}
type disablementServer = d.Server
type requestServer = r.Server
type testServers struct {
	*disablementServer
	*requestServer
}

func newTestServers(ops testOperations, principal func(context.Context) domain.Caller, clock func() time.Time) *testServers {
	return &testServers{d.NewServer(ops, principal, clock), r.NewServer(ops, principal, clock)}
}
func mountTestServices(mux *http.ServeMux, h *testServers, auth func(http.Handler) http.Handler) error {
	if err := d.Mount(mux, h.disablementServer, auth); err != nil {
		return err
	}
	return r.Mount(mux, h.requestServer, auth)
}

// Only the generated discovery/admin stubs use this test policy. Each real server mounts its own policy.
func testCallerPolicy(c domain.Caller, procedure string) error {
	switch procedure {
	case locationv1connect.LocationAdminServiceCreateLocationProcedure, locationv1connect.LocationAdminServiceUpdateLocationProcedure, locationv1connect.LocationAdminServiceArchiveLocationProcedure, locationv1connect.LocationAdminServiceUnarchiveLocationProcedure:
		if !c.IsAdmin() {
			return domain.ErrAccessDenied
		}
	}
	return nil
}
