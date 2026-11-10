package communication

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	communicationv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/communication/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/communication/v1/communicationv1connect"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/service"
)

type fakeEmailService struct {
	communicationv1connect.UnimplementedEmailServiceHandler
	got []*communicationv1.SendMagicLinkRequest
	err error
}

func (f *fakeEmailService) SendMagicLink(
	_ context.Context,
	req *connect.Request[communicationv1.SendMagicLinkRequest],
) (*connect.Response[communicationv1.SendMagicLinkResponse], error) {
	if f.err != nil {
		return nil, f.err
	}
	f.got = append(f.got, req.Msg)
	return connect.NewResponse(&communicationv1.SendMagicLinkResponse{}), nil
}

func newClient(t *testing.T, fake *fakeEmailService) *MagicLinks {
	t.Helper()
	_, handler := communicationv1connect.NewEmailServiceHandler(fake)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return NewMagicLinks(srv.Client(), srv.URL)
}

func TestSendMagicLinkSendsThePurpose(t *testing.T) {
	fake := &fakeEmailService{}
	links := newClient(t, fake)
	if err := links.SendMagicLink(context.Background(), "a@foc.local", "http://x/register?token=t",
		service.MagicLinkRegister); err != nil {
		t.Fatal(err)
	}
	if len(fake.got) != 1 || fake.got[0].GetEmail() != "a@foc.local" ||
		fake.got[0].GetPurpose() != communicationv1.MagicLinkPurpose_MAGIC_LINK_PURPOSE_REGISTER {
		t.Fatalf("got = %+v", fake.got)
	}
}

func TestSendMagicLinkReturnsCommunicationErrors(t *testing.T) {
	links := newClient(t, &fakeEmailService{err: connect.NewError(connect.CodeUnavailable, errors.New("smtp down"))})
	err := links.SendMagicLink(context.Background(), "a@foc.local", "http://x/login?token=t", service.MagicLinkLogin)
	if connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("err = %v", err)
	}
}
