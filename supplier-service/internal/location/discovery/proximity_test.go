package discovery

import (
	"math"
	"testing"
)

func TestProximityOriginValidation(t *testing.T) {
	for _, origin := range []*Coordinates{nil, {Latitude: 91}, {Longitude: -181}, {Latitude: math.NaN()}, {Longitude: math.Inf(1)}} {
		if _, _, _, err := normalizeList(Caller{}, ListRequest{Sort: SortByProximity, Origin: origin}); err == nil {
			t.Fatalf("accepted origin %+v", origin)
		}
	}
	for _, origin := range []*Coordinates{{}, {Latitude: -90, Longitude: 180}} {
		q, _, _, err := normalizeList(Caller{}, ListRequest{Sort: SortByProximity, Origin: origin})
		if err != nil || q.Origin != origin || q.Sort != SortByProximity {
			t.Fatalf("%+v %v", q, err)
		}
	}
}
