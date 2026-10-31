package admin

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"connectrpc.com/validate"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api/errs"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth"
	userv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/v1/userv1connect"
	"github.com/google/uuid"
)

type allowedDomainStub struct {
	domains []AllowedDomain
	err     error
	actorID uuid.UUID
	domain  string
	id      uuid.UUID
	called  bool
}

func (s *allowedDomainStub) ListAllowedDomains(context.Context) ([]AllowedDomain, error) {
	s.called = true
	return s.domains, s.err
}

func (s *allowedDomainStub) AllowDomain(_ context.Context, domain string, actorID uuid.UUID) error {
	s.called, s.domain, s.actorID = true, domain, actorID
	return s.err
}

func (s *allowedDomainStub) RemoveAllowedDomain(_ context.Context, id uuid.UUID) error {
	s.called, s.id = true, id
	return s.err
}

func allowedDomainClient(
	t *testing.T,
	logic AllowedDomainLogic,
	actorID uuid.UUID,
) userv1connect.AllowedDomainAdminServiceClient {
	t.Helper()
	_, handler := userv1connect.NewAllowedDomainAdminServiceHandler(
		&AllowedDomainHandler{Logic: logic}, connect.WithInterceptors(validate.NewInterceptor()))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if actorID != uuid.Nil {
			r = r.WithContext(auth.WithCaller(r.Context(), auth.Caller{ID: actorID.String()}))
		}
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	return userv1connect.NewAllowedDomainAdminServiceClient(server.Client(), server.URL)
}

func TestAllowedDomainRPCs(t *testing.T) {
	actorID, domainID := uuid.New(), uuid.New()
	logic := &allowedDomainStub{domains: []AllowedDomain{{ID: domainID, Domain: "example.com"}}}
	client := allowedDomainClient(t, logic, actorID)
	ctx := context.Background()
	list, err := client.ListAllowedDomains(ctx, connect.NewRequest(&userv1.ListAllowedDomainsRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Msg.AllowedDomains) != 1 || list.Msg.AllowedDomains[0].Id != domainID.String() ||
		list.Msg.AllowedDomains[0].Domain != "example.com" {
		t.Fatalf("unexpected domains: %v", list.Msg)
	}
	allowed, err := client.AllowDomain(ctx, connect.NewRequest(&userv1.AllowDomainRequest{Domain: "example.com"}))
	if err != nil || logic.actorID != actorID || logic.domain != "example.com" {
		t.Fatalf("allow domain: %v, actor=%v, domain=%q", err, logic.actorID, logic.domain)
	}
	removed, err := client.RemoveAllowedDomain(
		ctx,
		connect.NewRequest(&userv1.RemoveAllowedDomainRequest{Id: domainID.String()}),
	)
	if err != nil || logic.id != domainID {
		t.Fatalf("remove domain: %v, ID=%v", err, logic.id)
	}
	for _, header := range []http.Header{list.Header(), allowed.Header(), removed.Header()} {
		if header.Get("Cache-Control") != "no-store" {
			t.Fatal("response should not be cached")
		}
	}
}

func TestAllowedDomainRPCValidation(t *testing.T) {
	logic := &allowedDomainStub{}
	client := allowedDomainClient(t, logic, uuid.New())
	ctx := context.Background()
	if _, err := client.AllowDomain(
		ctx,
		connect.NewRequest(&userv1.AllowDomainRequest{Domain: "https://example.com"}),
	); connect.CodeOf(
		err,
	) != connect.CodeInvalidArgument {
		t.Fatalf("invalid hostname: %v", err)
	}
	for _, id := range []string{"invalid", uuid.Nil.String()} {
		if _, err := client.RemoveAllowedDomain(
			ctx,
			connect.NewRequest(&userv1.RemoveAllowedDomainRequest{Id: id}),
		); connect.CodeOf(
			err,
		) != connect.CodeInvalidArgument {
			t.Fatalf("invalid ID %q: %v", id, err)
		}
	}
	if logic.called {
		t.Fatal("invalid request reached the service")
	}
}

func TestAllowedDomainRPCErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		code connect.Code
	}{
		{"duplicate", errs.NewAlreadyExistsError("domain already allowed"), connect.CodeAlreadyExists},
		{"not found", errs.NewNotFoundError("allowed domain not found"), connect.CodeNotFound},
		{"internal", errors.New("private database details"), connect.CodeInternal},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := allowedDomainClient(t, &allowedDomainStub{err: tc.err}, uuid.New())
			_, err := client.AllowDomain(
				context.Background(),
				connect.NewRequest(&userv1.AllowDomainRequest{Domain: "example.com"}),
			)
			if connect.CodeOf(err) != tc.code {
				t.Fatalf("expected %v, got %v", tc.code, err)
			}
			if tc.code == connect.CodeInternal && strings.Contains(err.Error(), "private database details") {
				t.Fatal("internal error details leaked")
			}
		})
	}
	logic := &allowedDomainStub{}
	client := allowedDomainClient(t, logic, uuid.Nil)
	if _, err := client.ListAllowedDomains(
		context.Background(),
		connect.NewRequest(&userv1.ListAllowedDomainsRequest{}),
	); connect.CodeOf(
		err,
	) != connect.CodeUnauthenticated {
		t.Fatalf("missing actor: %v", err)
	}
	if logic.called {
		t.Fatal("unauthenticated request reached the service")
	}
}
