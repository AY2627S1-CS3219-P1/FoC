//go:build integration

package discovery

import (
	"context"
	"reflect"
	"testing"

	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/database/locationdb"
	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/testsupport/locationfixture"
)

func TestPostGISProximityPages(t *testing.T) {
	pool := locationfixture.SetupDatabase(t)
	ctx := context.Background()
	// Two equidistant names, plus a distant alphabetically earlier supplier.
	_, err := pool.Exec(ctx, `UPDATE locations SET coordinates = ST_SetSRID(ST_MakePoint(0,0),4326)::geography
WHERE id IN ($1,$2)`, cafeID, supperID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(
		ctx,
		`UPDATE locations SET coordinates = ST_SetSRID(ST_MakePoint(60,60),4326)::geography WHERE id=$1`,
		coopID,
	)
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(NewPostgresReader(locationdb.New(pool)))
	var ids []string
	for page := int32(1); page <= 3; page++ {
		result, err := service.List(ctx, Caller{}, ListRequest{Sort: SortByProximity,
			Origin: &Coordinates{}, SuppliersOnly: true, Page: page, PageSize: 1})
		if err != nil {
			t.Fatal(err)
		}
		if result.TotalItems != 3 || result.TotalPages != 3 {
			t.Fatalf("counts %+v", result)
		}
		ids = append(ids, result.Locations[0].ID)
	}
	if !reflect.DeepEqual(ids, []string{supperID, cafeID, coopID}) {
		t.Fatal(ids)
	}
	desc, err := service.List(ctx, Caller{}, ListRequest{Sort: SortByProximity,
		Origin: &Coordinates{}, SuppliersOnly: true, Descending: true})
	if err != nil || desc.Locations[0].ID != coopID || desc.Locations[1].ID != cafeID {
		t.Fatalf("descending %+v %v", desc, err)
	}
	search, err := service.List(ctx, Caller{}, ListRequest{Sort: SortByProximity,
		Origin: &Coordinates{}, SuppliersOnly: true, Search: "Cafe"})
	if err != nil || len(search.Locations) != 1 || search.Locations[0].ID != cafeID {
		t.Fatalf("search %+v %v", search, err)
	}

	// Equal distances and equal names must use ID in the chosen direction.
	_, err = pool.Exec(ctx, `UPDATE locations SET name='Supper Stretch' WHERE id=$1`, cafeID)
	if err != nil {
		t.Fatal(err)
	}
	for _, descending := range []bool{false, true} {
		result, err := service.List(ctx, Caller{}, ListRequest{Sort: SortByProximity,
			Origin: &Coordinates{}, SuppliersOnly: true, Search: "Supper", Descending: descending})
		want := []string{supperID, cafeID}
		if descending {
			want = []string{cafeID, supperID}
		}
		if err != nil || len(result.Locations) != 2 || result.Locations[0].ID != want[0] ||
			result.Locations[1].ID != want[1] {
			t.Fatalf("ID tie descending=%t: %+v %v", descending, result, err)
		}
	}
}
