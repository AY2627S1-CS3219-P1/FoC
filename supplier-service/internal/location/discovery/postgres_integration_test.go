//go:build integration

package discovery

import (
	"context"
	"errors"
	"testing"

	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/database/locationdb"
	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/testsupport/locationfixture"
	_ "github.com/jackc/pgx/v5/stdlib"
)

const (
	com2ID   = "a7ddb3ee-f24e-4464-bc33-6507ac5f5d68"
	pgpID    = "b1ddb3ee-f24e-4464-bc33-6507ac5f5d68"
	foodID   = "b526b558-e2ec-4db1-b873-13a9f490e07d"
	coffeeID = "29a7cb1e-44f2-4c68-825f-1ce39b78e44a"

	coopID     = "10000000-0000-4000-8000-000000000001"
	supperID   = "10000000-0000-4000-8000-000000000002"
	cafeID     = "10000000-0000-4000-8000-000000000003"
	loungeID   = "10000000-0000-4000-8000-000000000004"
	archivedID = "10000000-0000-4000-8000-000000000005"
)

func TestPostgresReader(t *testing.T) {
	pool := locationfixture.SetupDatabase(t)
	service := NewService(NewPostgresReader(locationdb.New(pool)))
	ctx := context.Background()
	user, admin := Caller{}, Caller{Admin: true}

	t.Run("get returns joined details", func(t *testing.T) {
		loc, err := service.Get(ctx, supperID)
		if err != nil {
			t.Fatal(err)
		}
		if loc.Name != "Supper Stretch" || loc.Building.Name != "PGP" || !loc.IsSupplier ||
			len(loc.Categories) != 2 || loc.Categories[0].Name != "Coffee" ||
			loc.OpensAt == nil || *loc.OpensAt != (Clock{Hour: 22}) || *loc.ClosesAt != (Clock{Hour: 2}) ||
			loc.Coordinates.Latitude != 1.2915 || loc.Coordinates.Longitude != 103.7805 ||
			loc.CurrentDisablement == nil || loc.CurrentDisablement.Reason != "Renovation" {
			t.Fatalf("location = %+v", loc)
		}
	})

	t.Run("get returns archived and reports missing", func(t *testing.T) {
		loc, err := service.Get(ctx, archivedID)
		if err != nil || loc.ArchivedAt == nil {
			t.Fatalf("archived location = %+v, err %v", loc, err)
		}
		if _, err := service.Get(ctx, "20000000-0000-4000-8000-000000000000"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("missing: err = %v", err)
		}
	})

	t.Run("disablements with equal start times resolve to the newest", func(t *testing.T) {
		for range 5 {
			loc, err := service.Get(ctx, cafeID)
			if err != nil || loc.CurrentDisablement == nil || loc.CurrentDisablement.Reason != "Newer" {
				t.Fatalf("location = %+v, err %v", loc, err)
			}
		}
	})

	t.Run("expired and cancelled disablements are not current", func(t *testing.T) {
		loc, err := service.Get(ctx, coopID)
		if err != nil || loc.CurrentDisablement != nil {
			t.Fatalf("location = %+v, err %v", loc, err)
		}
	})

	list := func(t *testing.T, caller Caller, req ListRequest) Page {
		t.Helper()
		page, err := service.List(ctx, caller, req)
		if err != nil {
			t.Fatal(err)
		}
		return page
	}

	cases := []struct {
		name   string
		caller Caller
		req    ListRequest
		want   []string
	}{
		{"default excludes archived, name ascending", user, ListRequest{}, []string{"Lounge", "NUS Co-op", "Supper Stretch", "The Cafe"}},
		{"name descending", user, ListRequest{Descending: true}, []string{"The Cafe", "Supper Stretch", "NUS Co-op", "Lounge"}},
		{"building ascending then name", user, ListRequest{Sort: SortByBuilding}, []string{"Lounge", "NUS Co-op", "Supper Stretch", "The Cafe"}},
		{"building descending", user, ListRequest{Sort: SortByBuilding, Descending: true}, []string{"The Cafe", "Supper Stretch", "NUS Co-op", "Lounge"}},
		{"case-insensitive substring", user, ListRequest{Search: "co-OP"}, []string{"NUS Co-op"}},
		{"fuzzy typo", user, ListRequest{Search: "supper strech"}, []string{"Supper Stretch"}},
		{"wildcards match literally", user, ListRequest{Search: "%"}, nil},
		{"building filter", user, ListRequest{BuildingID: ptr(pgpID)}, []string{"Supper Stretch", "The Cafe"}},
		{"category filter", user, ListRequest{CategoryID: ptr(coffeeID)}, []string{"Supper Stretch", "The Cafe"}},
		{"suppliers only", user, ListRequest{SuppliersOnly: true}, []string{"NUS Co-op", "Supper Stretch", "The Cafe"}},
		{"archived only", admin, ListRequest{Archive: ArchiveArchived}, []string{"Old Stall"}},
		{"all", admin, ListRequest{Archive: ArchiveAll}, []string{"Lounge", "NUS Co-op", "Old Stall", "Supper Stretch", "The Cafe"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			page := list(t, tc.caller, tc.req)
			if got := names(page.Locations); !equal(got, tc.want) {
				t.Fatalf("names = %v, want %v", got, tc.want)
			}
			if page.TotalItems != int64(len(tc.want)) {
				t.Fatalf("total = %d, want %d", page.TotalItems, len(tc.want))
			}
		})
	}

	t.Run("pages are stable and complete", func(t *testing.T) {
		var got []string
		for p := int32(1); p <= 3; p++ {
			page := list(t, user, ListRequest{Page: p, PageSize: 2})
			if page.TotalPages != 2 {
				t.Fatalf("total pages = %d, want 2", page.TotalPages)
			}
			got = append(got, names(page.Locations)...)
		}
		want := []string{"Lounge", "NUS Co-op", "Supper Stretch", "The Cafe"}
		if !equal(got, want) {
			t.Fatalf("pages = %v, want %v", got, want)
		}
	})

	t.Run("reference data", func(t *testing.T) {
		buildings, err := service.ListBuildings(ctx)
		if err != nil || len(buildings) != 2 || buildings[0].Name != "COM2" || buildings[0].Center.Longitude != 103.774 {
			t.Fatalf("buildings = %+v, err %v", buildings, err)
		}
		categories, err := service.ListCategories(ctx)
		if err != nil || len(categories) != 2 || categories[0].Name != "Coffee" {
			t.Fatalf("categories = %+v, err %v", categories, err)
		}
	})
}

func names(locations []Location) []string {
	var out []string
	for _, loc := range locations {
		out = append(out, loc.Name)
	}
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func ptr(s string) *string { return &s }
