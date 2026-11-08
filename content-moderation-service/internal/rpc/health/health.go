// Package health reports CMS's readiness over both the standard
// grpc.health.v1.Health protocol and the service-owned
// moderation.v1.HealthService contract the k8s probes and gateway call.
package health

import (
	"context"
	"fmt"
	"time"

	"connectrpc.com/connect"
	"connectrpc.com/grpchealth"
	moderationv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/moderation/v1"
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

// ModerationChecker adapts the same readiness signal to the generated
// moderation.v1.HealthService. The k8s readiness probe and the gateway route
// call this contract; without a mounted handler the pod can never become
// Ready (the probe requires HTTP 200 on /moderation.v1.HealthService/Check).
type ModerationChecker struct {
	DB Database
}

// Check implements moderationv1connect.HealthServiceHandler.
func (c *ModerationChecker) Check(
	ctx context.Context,
	req *connect.Request[moderationv1.CheckRequest],
) (*connect.Response[moderationv1.CheckResponse], error) {
	return respond(c.ping(ctx))
}

func (c *ModerationChecker) ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	return c.DB.Ping(ctx)
}

func respond(err error) (*connect.Response[moderationv1.CheckResponse], error) {
	status := "SERVING"
	if err != nil {
		status = "NOT_SERVING"
	}
	return connect.NewResponse(&moderationv1.CheckResponse{Status: status}), nil
}
