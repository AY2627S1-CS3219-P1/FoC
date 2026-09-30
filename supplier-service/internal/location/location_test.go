package location

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api"
)

type fakeReader struct {
	query     Query
	locations []Location
	total     int64
}

func (f *fakeReader) GetLocation(context.Context, string) (Location, error) {
	return Location{}, ErrNotFound
}

func (f *fakeReader) ListLocations(_ context.Context, query Query) ([]Location, int64, error) {
	f.query = query
	return f.locations, f.total, nil
}

func (f *fakeReader) ListBuildings(context.Context) ([]Building, error)  { return nil, nil }
func (f *fakeReader) ListCategories(context.Context) ([]Category, error) { return nil, nil }

func TestListAppliesDefaults(t *testing.T) {
	reader := &fakeReader{total: 41}
	page, err := NewService(reader).List(context.Background(), Caller{}, ListRequest{Search: "  coffee  "})
	if err != nil {
		t.Fatal(err)
	}

	want := Query{Search: "coffee", Archive: ArchiveActive, Sort: SortByName, Offset: 0, Limit: DefaultPageSize}
	if reader.query != want {
		t.Fatalf("query = %+v, want %+v", reader.query, want)
	}
	if page.Page != 1 || page.PageSize != DefaultPageSize || page.TotalItems != 41 || page.TotalPages != 3 {
		t.Fatalf("page = %+v", page)
	}
}

func TestListComputesOffset(t *testing.T) {
	reader := &fakeReader{}
	_, err := NewService(reader).List(context.Background(), Caller{}, ListRequest{Page: 3, PageSize: 10})
	if err != nil {
		t.Fatal(err)
	}
	if reader.query.Offset != 20 || reader.query.Limit != 10 {
		t.Fatalf("offset, limit = %d, %d, want 20, 10", reader.query.Offset, reader.query.Limit)
	}
}

func TestListEmptyResultHasZeroPages(t *testing.T) {
	page, err := NewService(&fakeReader{}).List(context.Background(), Caller{}, ListRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if page.TotalPages != 0 {
		t.Fatalf("total pages = %d, want 0", page.TotalPages)
	}
}

func TestListArchiveViewsRequireAdmin(t *testing.T) {
	for _, archive := range []ArchiveFilter{ArchiveArchived, ArchiveAll} {
		_, err := NewService(&fakeReader{}).List(context.Background(), Caller{}, ListRequest{Archive: archive})
		if !errors.Is(err, ErrPermissionDenied) {
			t.Fatalf("%s as user: err = %v, want permission denied", archive, err)
		}

		reader := &fakeReader{}
		if _, err := NewService(reader).List(context.Background(), Caller{Admin: true}, ListRequest{Archive: archive}); err != nil {
			t.Fatalf("%s as admin: %v", archive, err)
		}
		if reader.query.Archive != archive {
			t.Fatalf("archive = %q, want %q", reader.query.Archive, archive)
		}
	}
}

func TestListRejectsInvalidRequests(t *testing.T) {
	cases := map[string]struct {
		req     ListRequest
		message string
	}{
		"long search":        {ListRequest{Search: strings.Repeat("a", MaxSearchLength+1)}, "search has 201 characters; maximum is 200"},
		"negative page":      {ListRequest{Page: -1}, "page is -1; it cannot be negative"},
		"negative page size": {ListRequest{PageSize: -1}, "page_size is -1; it cannot be negative"},
		"large page":         {ListRequest{PageSize: MaxPageSize + 1}, "page_size is 101; maximum is 100"},
		"offset overflow":    {ListRequest{Page: 1 << 30, PageSize: MaxPageSize}, "exceeds the supported offset range"},
		"unknown sort":       {ListRequest{Sort: "rating"}, "sort value \"rating\" is invalid"},
		"unknown filter":     {ListRequest{Archive: "deleted"}, "archive value \"deleted\" is invalid"},
	}
	for name, tc := range cases {
		_, err := NewService(&fakeReader{}).List(context.Background(), Caller{Admin: true}, tc.req)
		var external api.ExternalError
		if !errors.As(err, &external) || external.Code() != http.StatusBadRequest {
			t.Errorf("%s: err = %v, want bad request", name, err)
		}
		if err == nil || !strings.Contains(err.Error(), tc.message) {
			t.Errorf("%s: err = %v, want message containing %q", name, err, tc.message)
		}
	}
}

func TestListSearchLimitAppliesAfterTrimming(t *testing.T) {
	search := "  " + strings.Repeat("a", MaxSearchLength) + "  "
	if _, err := NewService(&fakeReader{}).List(context.Background(), Caller{}, ListRequest{Search: search}); err != nil {
		t.Fatal(err)
	}
}

func TestEscapeLike(t *testing.T) {
	if got := escapeLike(`50%_off\`); got != `50\%\_off\\` {
		t.Fatalf("escapeLike = %q", got)
	}
}
