// Package clients adapts generated outbound RPCs to Order's consumer interfaces.
package clients

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"

	"connectrpc.com/connect"
	app "github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/order"
	creditpb "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/credit/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/credit/v1/creditv1connect"
	locationpb "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/supplier/location/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/supplier/location/v1/locationv1connect"
	"github.com/google/uuid"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
)

type userBearerKey struct{}

// WithUserBearer is called only after incoming HTTP authentication. The delegated
// credential is used for Supplier discovery and never persisted or sent to Credit.
func WithUserBearer(ctx context.Context, authorization string) context.Context {
	return context.WithValue(ctx, userBearerKey{}, authorization)
}

func NoRedirect(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }

type Supplier struct {
	Client locationv1connect.LocationDiscoveryServiceClient
}

func (s *Supplier) GetLocation(ctx context.Context, id string) (app.SelectableLocation, error) {
	bearer, _ := ctx.Value(userBearerKey{}).(string)
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
		Snapshot: app.LocationSnapshot{LocationID: locationID.String(), Name: loc.GetName(), BuildingName: loc.GetBuilding().GetName(), Floor: loc.Floor},
		Archived: loc.ArchivedAt != nil,
	}, nil
}

type Credit struct {
	Client    creditv1connect.CreditReservationServiceClient
	TokenFile string
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

func authorize[T any](request *connect.Request[T], file string) error {
	token, err := ReadServiceToken(file)
	if err != nil {
		return err
	}
	request.Header().Set("Authorization", "Bearer "+token)
	return nil
}

func (c *Credit) Reserve(ctx context.Context, intent app.ReservationIntent) error {
	request := connect.NewRequest(&creditpb.ReserveRequest{OrderId: intent.OrderID, RequesterId: intent.RequesterID, Amount: intent.Amount})
	if err := authorize(request, c.TokenFile); err != nil {
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
	if v == nil || v.OrderId != intent.OrderID || v.RequesterId != intent.RequesterID || v.OriginalAmount != intent.Amount {
		return app.ErrUnavailable
	}
	return nil
}

func (c *Credit) ReleaseExpired(ctx context.Context, id string) error {
	request := connect.NewRequest(&creditpb.ReleaseReservationRequest{OrderId: id, Reason: creditpb.ReleaseReason_RELEASE_REASON_EXPIRED})
	if err := authorize(request, c.TokenFile); err != nil {
		return err
	}
	response, err := c.Client.ReleaseReservation(ctx, request)
	if err != nil {
		return app.ErrUnavailable
	}
	v := response.Msg.GetReservation()
	if v == nil || v.OrderId != id || v.Status != creditpb.ReservationStatus_RESERVATION_STATUS_RELEASED || v.ReservedAmount != 0 {
		return app.ErrUnavailable
	}
	return nil
}

func (c *Credit) GetReservation(ctx context.Context, id string) (app.ReservationView, error) {
	request := connect.NewRequest(&creditpb.GetReservationRequest{OrderId: id})
	if err := authorize(request, c.TokenFile); err != nil {
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
	if v == nil || v.OrderId != id || v.OriginalAmount < 0 || v.ReservedAmount < 0 {
		return app.ReservationView{}, app.ErrUnavailable
	}
	view := app.ReservationView{OrderID: v.OrderId, RequesterID: v.RequesterId, OriginalAmount: v.OriginalAmount, ReservedAmount: v.ReservedAmount}
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
