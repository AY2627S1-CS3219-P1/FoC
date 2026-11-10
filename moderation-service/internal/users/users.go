// Package users reads what Moderation Service needs from User Service through
// its internal listener: participant names and roles, and the facts that
// decide whether a review or warning can be reported or appealed.
package users

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api/errs"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth/tokenclaims"
	userv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/v1/userv1connect"
)

var (
	// ErrNotFound reports a review or warning User Service does not know.
	ErrNotFound = errors.New("not found in User Service")
	// ErrUnavailable reports that User Service could not answer.
	ErrUnavailable = errors.New("user service unavailable")
)

// maxBatch matches the GetUsers request limit.
const maxBatch = 100

// Person is a user as Moderation Service shows and checks them.
type Person struct {
	ID   uuid.UUID
	Name string
	Role tokenclaims.Role
}

// IsAdmin reports an administrator or super administrator.
func (p Person) IsAdmin() bool {
	return p.Role == tokenclaims.RoleAdmin || p.Role == tokenclaims.RoleSuperAdmin
}

// IsSuspended reports a suspended account.
func (p Person) IsSuspended() bool { return p.Role == tokenclaims.RoleSuspendedUser }

// ReviewFacts describes a review. RemovalReportID is set while a report's
// outcome keeps the review removed.
type ReviewFacts struct {
	ReviewerID      uuid.UUID
	RevieweeID      uuid.UUID
	OrderID         uuid.UUID
	Removed         bool
	RemovalReportID uuid.UUID
}

// WarningFacts describes an account warning. ReportID is set when a report
// issued it.
type WarningFacts struct {
	UserID    uuid.UUID
	RequestID uuid.UUID
	ReportID  uuid.UUID
	Active    bool
}

// Client calls UserInternalService.
type Client struct {
	rpc userv1connect.UserInternalServiceClient
}

// NewClient wraps the generated client.
func NewClient(rpc userv1connect.UserInternalServiceClient) *Client { return &Client{rpc: rpc} }

// People returns the users that exist among ids, keyed by ID.
func (c *Client) People(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]Person, error) {
	people := make(map[uuid.UUID]Person, len(ids))
	unique := dedupe(ids)
	for start := 0; start < len(unique); start += maxBatch {
		batch := unique[start:min(start+maxBatch, len(unique))]
		raw := make([]string, len(batch))
		for i, id := range batch {
			raw[i] = id.String()
		}

		resp, err := c.rpc.GetUsers(ctx, connect.NewRequest(&userv1.GetUsersRequest{Ids: raw}))
		if err != nil {
			return nil, fmt.Errorf("%w: get users: %v", ErrUnavailable, err)
		}
		for _, user := range resp.Msg.GetUsers() {
			id, err := uuid.Parse(user.GetId())
			if err != nil {
				continue
			}
			people[id] = Person{ID: id, Name: user.GetDisplayName(), Role: tokenclaims.Role(user.GetRole())}
		}
	}
	return people, nil
}

// ReviewFacts reads one review's facts.
func (c *Client) ReviewFacts(ctx context.Context, id uuid.UUID) (ReviewFacts, error) {
	resp, err := c.rpc.GetReviewFacts(ctx, connect.NewRequest(&userv1.GetReviewFactsRequest{ReviewId: id.String()}))
	if err != nil {
		return ReviewFacts{}, classify("review facts", err)
	}
	msg := resp.Msg
	return ReviewFacts{
		ReviewerID:      parseID(msg.GetReviewerId()),
		RevieweeID:      parseID(msg.GetRevieweeId()),
		OrderID:         parseID(msg.GetOrderId()),
		Removed:         msg.GetRemoved(),
		RemovalReportID: parseID(msg.GetRemovalReportId()),
	}, nil
}

// WarningFacts reads one warning's facts.
func (c *Client) WarningFacts(ctx context.Context, id uuid.UUID) (WarningFacts, error) {
	resp, err := c.rpc.GetWarningFacts(ctx, connect.NewRequest(&userv1.GetWarningFactsRequest{WarningId: id.String()}))
	if err != nil {
		return WarningFacts{}, classify("warning facts", err)
	}
	msg := resp.Msg
	return WarningFacts{
		UserID:    parseID(msg.GetUserId()),
		RequestID: parseID(msg.GetRequestId()),
		ReportID:  parseID(msg.GetReportId()),
		Active:    msg.GetActive(),
	}, nil
}

// PeopleSource is the part of Client that names users.
type PeopleSource interface {
	People(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]Person, error)
}

// Names returns display names for ids. Names are only shown, so when User
// Service cannot answer it logs and returns none rather than failing a read.
func Names(ctx context.Context, source PeopleSource, ids []uuid.UUID) map[uuid.UUID]string {
	names := make(map[uuid.UUID]string, len(ids))
	people, err := source.People(ctx, ids)
	if err != nil {
		slog.WarnContext(ctx, "User Service names unavailable; showing none", "error", err)
		return names
	}
	for id, person := range people {
		names[id] = person.Name
	}
	return names
}

// ExternalError maps a lookup failure to the error a caller should see.
func ExternalError(err error, notFound string) error {
	switch {
	case errors.Is(err, ErrNotFound):
		return errs.WrapNotFoundError(err, notFound)
	case errors.Is(err, ErrUnavailable):
		return connect.NewError(connect.CodeUnavailable, errors.New("user service is unavailable; try again"))
	default:
		return err
	}
}

func classify(operation string, err error) error {
	if connect.CodeOf(err) == connect.CodeNotFound {
		return fmt.Errorf("%s: %w", operation, ErrNotFound)
	}
	return fmt.Errorf("%w: %s: %v", ErrUnavailable, operation, err)
}

func parseID(value string) uuid.UUID {
	id, err := uuid.Parse(value)
	if err != nil {
		return uuid.Nil
	}
	return id
}

func dedupe(ids []uuid.UUID) []uuid.UUID {
	seen := make(map[uuid.UUID]bool, len(ids))
	out := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		if id != uuid.Nil && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}
