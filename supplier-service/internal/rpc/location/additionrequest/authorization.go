package additionrequest

import (
	"buf.build/go/protovalidate"
	"connectrpc.com/connect"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/supplier/location/v1/locationv1connect"
	domain "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/location/shared"
	rpcshared "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/rpc/location/shared"
)

func CallerInterceptor(principal Principal, validator protovalidate.Validator) connect.Interceptor {
	return rpcshared.CallerInterceptor(principal, validator, authorizeCaller)
}
func authorizeCaller(c domain.Caller, procedure string) error {
	switch procedure {
	case locationv1connect.LocationAdditionRequestServiceApproveLocationAdditionRequestProcedure, locationv1connect.LocationAdditionRequestServiceRejectLocationAdditionRequestProcedure:
		if !c.IsAdmin() {
			return domain.ErrAccessDenied
		}
	case locationv1connect.LocationAdditionRequestServiceSubmitLocationAdditionRequestProcedure:
		if c.Role == "suspended_user" {
			return domain.ErrAccessDenied
		}
	}
	return nil
}
