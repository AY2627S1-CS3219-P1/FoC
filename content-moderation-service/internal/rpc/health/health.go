// Package health reports CMS's readiness over the standard
// grpc.health.v1.Health protocol.
package health

import (
	"context"
	"fmt"
	"time"

	"connectrpc.com/connect"
	"connectrpc.com/grpchealth"
)

type Database interface{ Ping(context.Context) error }

// Health reflects database availability, not inference readiness.
type Checker struct {
	DB       Database
	Services []string
}

func (c *Checker) Check(ctx context.Context, req *grpchealth.CheckRequest) (*grpchealth.CheckResponse, error) {
	if req.Service != "" && !c.serves(req.Service) {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("unknown service %q", req.Service))
	}

	status := grpchealth.StatusServing
	if err := c.ping(ctx); err != nil {
		status = grpchealth.StatusNotServing
	}
	return &grpchealth.CheckResponse{Status: status}, nil
}

func (c *Checker) ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	return c.DB.Ping(ctx)
}

func (c *Checker) serves(service string) bool {
	for _, s := range c.Services {
		if s == service {
			return true
		}
	}
	return false
}
