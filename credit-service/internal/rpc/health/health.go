// Package health reports Credit's readiness over the standard
// grpc.health.v1.Health protocol.
package health

import (
	"context"
	"fmt"
	"time"

	"connectrpc.com/connect"
	"connectrpc.com/grpchealth"
)

// Database lists only the dependency health consumes.
type Database interface{ Ping(context.Context) error }

// Checker reports the process, and each service it names, as serving while
// the database answers.
type Checker struct {
	DB       Database
	Services []string
}

func (c *Checker) Check(ctx context.Context, req *grpchealth.CheckRequest) (*grpchealth.CheckResponse, error) {
	if req.Service != "" && !c.serves(req.Service) {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("unknown service %q", req.Service))
	}

	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	if err := c.DB.Ping(ctx); err != nil {
		return &grpchealth.CheckResponse{Status: grpchealth.StatusNotServing}, nil
	}
	return &grpchealth.CheckResponse{Status: grpchealth.StatusServing}, nil
}

func (c *Checker) serves(service string) bool {
	for _, s := range c.Services {
		if s == service {
			return true
		}
	}
	return false
}
