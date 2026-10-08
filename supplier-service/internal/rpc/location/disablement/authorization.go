package disablement

import (
	"buf.build/go/protovalidate"
	"connectrpc.com/connect"
	domain "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/location/shared"
	rpcshared "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/rpc/location/shared"
)

func CallerInterceptor(principal Principal, validator protovalidate.Validator) connect.Interceptor {
	return rpcshared.CallerInterceptor(principal, validator, authorizeCaller)
}
func authorizeCaller(c domain.Caller, procedure string) error {
	if !c.IsAdmin() {
		return domain.ErrAccessDenied
	}
	return nil
}
