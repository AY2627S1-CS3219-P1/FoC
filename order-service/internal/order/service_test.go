package order

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

type readFake struct {
	historical   bool
	order        Order
	getErr       error
	page         OrderPage
	listErr      error
	offers       []Offer
	offersErr    error
	getID        string
	listQuery    ReadQuery
	offersCaller Caller
	offersID     string
	offersStatus OfferStatus
	counts       map[Status]int64
	counted      bool
	dayCounts    map[Date]int64
	dayRange     [2]time.Time
}

func (f *readFake) Get(_ context.Context, id string) (Order, error) {
	f.getID = id
	return f.order, f.getErr
}
func (f *readFake) GetWithParticipation(ctx context.Context, id, _ string) (Order, bool, error) {
	o, err := f.Get(ctx, id)
	return o, f.historical, err
}
func (f *readFake) List(_ context.Context, q ReadQuery) (OrderPage, error) {
	f.listQuery = q
	return f.page, f.listErr
}
func (f *readFake) Offers(_ context.Context, caller Caller, id string, status OfferStatus) ([]Offer, error) {
	f.offersCaller, f.offersID, f.offersStatus = caller, id, status
	return f.offers, f.offersErr
}

func (f *readFake) CountByStatus(context.Context) (map[Status]int64, error) {
	f.counted = true
	return f.counts, nil
}

func (f *readFake) CountByDay(_ context.Context, from, to time.Time) (map[Date]int64, error) {
	f.dayRange = [2]time.Time{from, to}
	return f.dayCounts, nil
}

func strptr(v string) *string { return &v }

// NFR-05.2.3/NFR-05.2.5 (../../REQUIREMENTS.md#nfr-05-auth): read authorization
// policy uses supplied participant data; this fake does not prove database reads.
func TestGetOrderAccessAndRedaction(t *testing.T) {
	cases := []struct {
		name       string
		caller     Caller
		status     Status
		wantErr    error
		wantSecret bool
	}{
		{
			name:       "requester sees instructions",
			caller:     Caller{ID: "requester"},
			status:     AwaitingPickup,
			wantSecret: true,
		},
		{
			name:       "suspended requester keeps participant history",
			caller:     Caller{ID: "requester", Suspended: true},
			status:     Cancelled,
			wantSecret: true,
		},
		{name: "courier sees instructions", caller: Caller{ID: "courier"}, status: Delivering, wantSecret: true},
		{name: "admin without case grant is redacted", caller: Caller{ID: "admin", Admin: true}, status: Cancelled},
		{name: "open browser receives redacted instructions", caller: Caller{ID: "browser"}, status: AwaitingCourier},
		{
			name:    "closed stranger cannot inspect",
			caller:  Caller{ID: "stranger"},
			status:  Delivered,
			wantErr: ErrNotFound,
		},
		{
			name:    "expired open order is invisible",
			caller:  Caller{ID: "browser"},
			status:  AwaitingCourier,
			wantErr: ErrNotFound,
		},
		{
			name:    "suspended stranger cannot browse",
			caller:  Caller{ID: "suspended", Suspended: true},
			status:  AwaitingCourier,
			wantErr: ErrNotFound,
		},
		{name: "empty identity forbidden", caller: Caller{}, status: AwaitingCourier, wantErr: ErrForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			expiresAt := time.Now().Add(time.Hour)
			if tc.name == "expired open order is invisible" {
				expiresAt = time.Now().Add(-time.Hour)
			}
			f := &readFake{
				order: Order{
					ID:                   "o1",
					RequesterID:          "requester",
					CourierID:            strptr("courier"),
					Status:               tc.status,
					DeliveryInstructions: strptr("leave at desk"),
					ExpiresAt:            expiresAt,
				},
			}
			got, err := NewService(f).GetOrder(context.Background(), tc.caller, "o1")
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("GetOrder error = %v, want %v", err, tc.wantErr)
			}
			if tc.wantErr == nil && (got.DeliveryInstructions != nil) != tc.wantSecret {
				t.Fatalf("instructions = %v, want visible %v", got.DeliveryInstructions, tc.wantSecret)
			}
			if tc.caller.ID != "" && f.getID != "o1" {
				t.Fatalf("reader got id %q", f.getID)
			}
		})
	}
}

func TestListReadsApplyDefaultsAndForwardFilters(t *testing.T) {
	f := &readFake{
		page: OrderPage{
			Orders: []Order{{ID: "public", Status: AwaitingCourier, DeliveryInstructions: strptr("private")}},
		},
	}
	s := NewService(f)
	caller := Caller{ID: "browse"}
	result, err := s.ListOpenOrders(context.Background(), caller, ListRequest{Page: Page{}})
	if err != nil {
		t.Fatal(err)
	}
	if f.listQuery.ListRequest.Page != (Page{Number: 1, Size: 20}) || !f.listQuery.Open ||
		f.listQuery.Caller != caller {
		t.Fatalf("open query = %+v", f.listQuery)
	}
	if result.Orders[0].DeliveryInstructions != nil {
		t.Fatal("open browser received delivery instructions")
	}

	filter := ListRequest{
		Page:                 Page{Number: 2, Size: 7},
		Status:               AwaitingPickup,
		Role:                 CourierRole,
		PickupID:             "p",
		DeliveryBuildingName: "COM3",
	}
	if _, err := s.ListMyOrders(
		context.Background(),
		Caller{ID: "suspended-user", Suspended: true},
		filter,
	); err != nil {
		t.Fatalf("suspended participant history read: %v", err)
	}
	want := filter
	want.Page = Page{Number: 2, Size: 7}
	if f.listQuery.UserID != "suspended-user" || f.listQuery.Open || !reflect.DeepEqual(f.listQuery.ListRequest, want) {
		t.Fatalf("my orders query = %+v", f.listQuery)
	}
}

// NFR-05.2.3/NFR-05.2.5 (../../REQUIREMENTS.md#nfr-05-auth): administrative
// read authorization precedes validation for invalid administrative requests.
func TestListValidationAndAdminAuthorizationOrder(t *testing.T) {
	f := &readFake{}
	s := NewService(f)
	if _, err := s.ListOpenOrders(
		context.Background(),
		Caller{ID: "suspended", Suspended: true},
		ListRequest{},
	); !errors.Is(
		err,
		ErrForbidden,
	) {
		t.Fatalf("suspended browse error = %v", err)
	}
	if _, err := s.ListMyOrders(
		context.Background(),
		Caller{ID: "u"},
		ListRequest{Page: Page{Number: -1}},
	); !errors.Is(
		err,
		ErrInvalid,
	) {
		t.Fatalf("invalid page error = %v", err)
	}
	if _, err := s.ListMyOrders(
		context.Background(),
		Caller{ID: "u"},
		ListRequest{Page: Page{Size: 101}},
	); !errors.Is(
		err,
		ErrInvalid,
	) {
		t.Fatalf("oversized page error = %v", err)
	}
	if _, err := s.ListMyOrders(
		context.Background(),
		Caller{ID: "u"},
		ListRequest{Status: "unknown"},
	); !errors.Is(
		err,
		ErrInvalid,
	) {
		t.Fatalf("invalid status error = %v", err)
	}
	if _, err := s.ListUserOrders(
		context.Background(),
		Caller{ID: "u"},
		"",
		ListRequest{Page: Page{Number: -1}},
	); !errors.Is(
		err,
		ErrForbidden,
	) {
		t.Fatalf("non-admin error must precede validation: %v", err)
	}
	if _, err := s.ListUserOrders(
		context.Background(),
		Caller{ID: "admin", Admin: true},
		"",
		ListRequest{},
	); !errors.Is(
		err,
		ErrInvalid,
	) {
		t.Fatalf("admin empty target error = %v", err)
	}
	if f.listQuery.UserID != "" {
		t.Fatal("reader called for unauthorized or invalid request")
	}
}

func TestOfferQueryIsForwardedToReader(t *testing.T) {
	f := &readFake{offers: []Offer{{ID: "offer", OrderID: "o1"}}}
	c := Caller{ID: "requester", Suspended: true}
	got, err := NewService(f).ListOffers(context.Background(), c, "o1", OfferAccepted)
	if err != nil || len(got) != 1 {
		t.Fatalf("ListOffers = (%v, %v)", got, err)
	}
	if f.offersCaller != c || f.offersID != "o1" || f.offersStatus != OfferAccepted {
		t.Fatalf("reader offer query = (%+v, %q, %q)", f.offersCaller, f.offersID, f.offersStatus)
	}
	if _, err := NewService(
		f,
	).ListOffers(context.Background(), Caller{ID: "u"}, "o1", OfferStatus("invalid")); !errors.Is(
		err,
		ErrInvalid,
	) {
		t.Fatalf("invalid offer status error = %v", err)
	}
}

func TestCountOrdersByStatusRequiresAdmin(t *testing.T) {
	f := &readFake{counts: map[Status]int64{AwaitingCourier: 3}}
	s := NewService(f)

	if _, err := s.CountOrdersByStatus(context.Background(), Caller{ID: "u"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("non-admin error = %v", err)
	}
	if f.counted {
		t.Fatal("reader called for non-admin")
	}

	got, err := s.CountOrdersByStatus(context.Background(), Caller{ID: "a", Admin: true})
	if err != nil || !reflect.DeepEqual(got, f.counts) {
		t.Fatalf("admin counts = (%v, %v)", got, err)
	}
}

func TestCountOrdersByDay(t *testing.T) {
	admin := Caller{ID: "a", Admin: true}
	ctx := context.Background()
	start, end := Date{2026, time.February, 27}, Date{2026, time.March, 2}

	f := &readFake{dayCounts: map[Date]int64{{2026, time.February, 28}: 4, {2026, time.March, 2}: 1}}
	s := NewService(f)
	if _, err := s.CountOrdersByDay(ctx, Caller{ID: "u"}, start, end); !errors.Is(err, ErrForbidden) {
		t.Fatalf("non-admin error = %v", err)
	}

	invalid := []struct {
		name       string
		start, end Date
	}{
		{"end before start", end, start},
		{"not a calendar day", Date{2026, time.February, 30}, end},
		{"zero date", Date{}, end},
		{"over 366 days", Date{2025, time.January, 1}, Date{2026, time.January, 2}},
	}
	for _, c := range invalid {
		if _, err := s.CountOrdersByDay(ctx, admin, c.start, c.end); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%s error = %v", c.name, err)
		}
	}
	if !f.dayRange[0].IsZero() {
		t.Fatal("reader called for an invalid request")
	}

	got, err := s.CountOrdersByDay(ctx, admin, start, end)
	if err != nil {
		t.Fatal(err)
	}
	want := []DailyCount{
		{Date{2026, time.February, 27}, 0},
		{Date{2026, time.February, 28}, 4},
		{Date{2026, time.March, 1}, 0},
		{Date{2026, time.March, 2}, 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("days = %v, want %v", got, want)
	}

	// The range is half-open over Singapore midnights.
	from := time.Date(2026, time.February, 26, 16, 0, 0, 0, time.UTC)
	to := time.Date(2026, time.March, 2, 16, 0, 0, 0, time.UTC)
	if !f.dayRange[0].Equal(from) || !f.dayRange[1].Equal(to) {
		t.Fatalf("reader range = %v", f.dayRange)
	}

	if days, err := s.CountOrdersByDay(
		ctx,
		admin,
		Date{2025, time.January, 1},
		Date{2026, time.January, 1},
	); err != nil || len(days) != MaxDailyRange {
		t.Fatalf("366-day range = %d days, %v", len(days), err)
	}
}
