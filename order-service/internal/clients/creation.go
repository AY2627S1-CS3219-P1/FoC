// Package clients adapts generated outbound RPCs to Order's consumer interfaces.
package clients

import (
	"context"
	"errors"
	"github.com/golang-jwt/jwt/v5"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"connectrpc.com/connect"
	app "github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/order"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth"
	creditpb "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/credit/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/credit/v1/creditv1connect"
	locationpb "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/supplier/location/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/supplier/location/v1/locationv1connect"
	"github.com/google/uuid"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
)

func NoRedirect(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }

type Supplier struct {
	Client locationv1connect.LocationDiscoveryServiceClient
}

func (s *Supplier) GetLocation(ctx context.Context, id string) (app.SelectableLocation, error) {
	bearer, _ := auth.UserBearerFromContext(ctx)
	if bearer == "" {
		return app.SelectableLocation{}, app.ErrUnavailable
	}
	request := connect.NewRequest(&locationpb.GetLocationRequest{Id: id})
	request.Header().Set("Authorization", bearer)
	response, err := s.Client.GetLocation(ctx, request)
	if err != nil {
		if connect.CodeOf(err) == connect.CodeNotFound {
			return app.SelectableLocation{}, app.ErrNotFound
		}
		return app.SelectableLocation{}, app.ErrUnavailable
	}
	loc := response.Msg.GetLocation()
	if loc == nil || loc.GetBuilding() == nil || loc.GetName() == "" || loc.GetBuilding().GetName() == "" {
		return app.SelectableLocation{}, app.ErrUnavailable
	}
	locationID, err := uuid.Parse(loc.GetId())
	if err != nil || locationID.String() != id {
		return app.SelectableLocation{}, app.ErrUnavailable
	}
	return app.SelectableLocation{
		Snapshot: app.LocationSnapshot{
			LocationID:   locationID.String(),
			Name:         loc.GetName(),
			BuildingName: loc.GetBuilding().GetName(),
			Floor:        loc.Floor,
		},
		Archived: loc.ArchivedAt != nil,
	}, nil
}

type Credit struct {
	Client creditv1connect.CreditReservationServiceClient
	Tokens TokenSource
}

// ReadServiceToken reads on every dispatch, so an atomic file replacement rotates
// credentials without restarting or embedding secrets in durable financial work.
func ReadServiceToken(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", app.ErrUnavailable
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, 8193))
	if err != nil || len(data) > 8192 {
		return "", app.ErrUnavailable
	}
	token := strings.TrimSpace(string(data))
	if token == "" || strings.IndexFunc(token, func(r rune) bool { return r <= 32 || r >= 127 }) >= 0 {
		return "", app.ErrUnavailable
	}
	return token, nil
}

// TokenSource is supplied by the #29 workload-identity deployment. User JWTs
// never enter this boundary. FileTokens is a credential delivery adapter, not an
// issuer or a verifier for the receiving Credit service.
type TokenSource interface {
	Token(context.Context) (string, error)
}

type FileTokens struct{ Path string }

func (s FileTokens) Token(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	value, err := ReadServiceToken(s.Path)
	if err != nil {
		return "", err
	}
	// The mounted file is trusted deployment input. Claim inspection only prevents
	// stale/wrong-purpose dispatch; Credit must cryptographically verify the JWT.
	claims := jwt.MapClaims{}
	if _, _, err := jwt.NewParser().ParseUnverified(value, claims); err != nil {
		return "", app.ErrUnavailable
	}
	expiry, err := claims.GetExpirationTime()
	if err != nil || expiry == nil || !expiry.After(time.Now()) {
		return "", app.ErrUnavailable
	}
	subject, err := claims.GetSubject()
	if err != nil || subject != "order-service" {
		return "", app.ErrUnavailable
	}
	audience, err := claims.GetAudience()
	if err != nil {
		return "", app.ErrUnavailable
	}
	for _, target := range audience {
		if target == "credit-service" {
			return value, nil
		}
	}
	return "", app.ErrUnavailable
}

func authorize[T any](ctx context.Context, request *connect.Request[T], source TokenSource) error {
	if source == nil {
		return app.ErrUnavailable
	}
	token, err := source.Token(ctx)
	if err != nil {
		return err
	}
	request.Header().Set("Authorization", "Bearer "+token)
	return nil
}

func (c *Credit) Reserve(ctx context.Context, intent app.ReservationIntent) error {
	request := connect.NewRequest(
		&creditpb.ReserveRequest{RequestId: intent.OrderID, RequesterId: intent.RequesterID, Amount: intent.Amount},
	)
	if err := authorize(ctx, request, c.Tokens); err != nil {
		return err
	}
	response, err := c.Client.Reserve(ctx, request)
	if err != nil {
		if hasCreditReason(err, connect.CodeFailedPrecondition, "INSUFFICIENT_CREDITS") {
			return app.ErrCreditInsufficient
		}
		return app.ErrUnavailable
	}
	v := response.Msg.GetReservation()
	if v == nil || v.RequestId != intent.OrderID || v.RequesterId != intent.RequesterID ||
		v.OriginalAmount != intent.Amount {
		return app.ErrUnavailable
	}
	return nil
}

func (c *Credit) ReleaseExpired(ctx context.Context, id string) error {
	request := connect.NewRequest(
		&creditpb.ReleaseReservationRequest{RequestId: id, Reason: creditpb.ReleaseReason_RELEASE_REASON_EXPIRED},
	)
	if err := authorize(ctx, request, c.Tokens); err != nil {
		return err
	}
	response, err := c.Client.ReleaseReservation(ctx, request)
	if err != nil {
		return app.ErrUnavailable
	}
	v := response.Msg.GetReservation()
	if v == nil || v.RequestId != id || v.Status != creditpb.ReservationStatus_RESERVATION_STATUS_RELEASED ||
		v.ReservedAmount != 0 {
		return app.ErrUnavailable
	}
	return nil
}

func (c *Credit) GetReservation(ctx context.Context, id string) (app.ReservationView, error) {
	request := connect.NewRequest(&creditpb.GetReservationRequest{RequestId: id})
	if err := authorize(ctx, request, c.Tokens); err != nil {
		return app.ReservationView{}, err
	}
	response, err := c.Client.GetReservation(ctx, request)
	if err != nil {
		if hasCreditReason(err, connect.CodeNotFound, "RESERVATION_NOT_FOUND") {
			return app.ReservationView{}, app.ErrNotFound
		}
		return app.ReservationView{}, app.ErrUnavailable
	}
	v := response.Msg.GetReservation()
	if v == nil || v.RequestId != id || v.OriginalAmount < 0 || v.ReservedAmount < 0 {
		return app.ReservationView{}, app.ErrUnavailable
	}
	view := app.ReservationView{
		OrderID:        v.RequestId,
		RequesterID:    v.RequesterId,
		OriginalAmount: v.OriginalAmount,
		ReservedAmount: v.ReservedAmount,
	}
	switch v.Status {
	case creditpb.ReservationStatus_RESERVATION_STATUS_HELD:
		if v.ActiveAdjustment != nil || v.Settlement != nil {
			return app.ReservationView{}, app.ErrUnavailable
		}
		view.State = app.ReservationHeld
	case creditpb.ReservationStatus_RESERVATION_STATUS_RELEASED:
		if v.ReservedAmount != 0 || v.ActiveAdjustment != nil {
			return app.ReservationView{}, app.ErrUnavailable
		}
		view.State = app.ReservationIsReleased
	default:
		return app.ReservationView{}, app.ErrUnavailable
	}
	return view, nil
}

func hasCreditReason(err error, code connect.Code, reason string) bool {
	var ce *connect.Error
	if !errors.As(err, &ce) || ce.Code() != code {
		return false
	}
	for _, detail := range ce.Details() {
		value, err := detail.Value()
		if err != nil {
			continue
		}
		info, ok := value.(*errdetails.ErrorInfo)
		if ok && info.Domain == "credit.v1" && info.Reason == reason {
			return true
		}
	}
	return false
}

var _ app.CreationLocations = (*Supplier)(nil)
var _ app.CreationCredit = (*Credit)(nil)
