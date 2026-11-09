package pagination

import (
	"errors"
	"math"
	"testing"
)

func TestNewDefaults(t *testing.T) {
	for _, tc := range []struct {
		name           string
		page, pageSize int64
		wantPage       int64
		wantPageSize   int64
		wantOffset     int64
	}{
		{"zero page and size take the defaults", 0, 0, 1, 20, 0},
		{"negative page and size take the defaults", -3, -7, 1, 20, 0},
		{"explicit page and size are kept", 3, 10, 3, 10, 20},
		{"page size above the maximum is clamped", 2, 500, 2, 100, 100},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, err := New(tc.page, tc.pageSize)
			if err != nil {
				t.Fatalf("New(%d, %d): %v", tc.page, tc.pageSize, err)
			}
			if p.Page() != tc.wantPage || p.PageSize() != tc.wantPageSize || p.Offset() != tc.wantOffset {
				t.Fatalf(
					"New(%d, %d) = page %d size %d offset %d, want page %d size %d offset %d",
					tc.page,
					tc.pageSize,
					p.Page(),
					p.PageSize(),
					p.Offset(),
					tc.wantPage,
					tc.wantPageSize,
					tc.wantOffset,
				)
			}
		})
	}
}

func TestNewRejectsOffsetOverflow(t *testing.T) {
	for _, tc := range []struct {
		name           string
		page, pageSize int64
	}{
		{"first overflow at size two", math.MaxInt64/2 + 2, 2},
		{"first overflow at the default size", math.MaxInt64/20 + 2, 20},
		{"first overflow at the maximum size", math.MaxInt64/100 + 2, 100},
		{"maximum page at the maximum size", math.MaxInt64, 100},
		{"overflow that would wrap to zero", 1<<62 + 1, 4},
		{"overflow that would wrap to positive", 1<<62 + 2, 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := New(tc.page, tc.pageSize)
			if !errors.Is(err, ErrOffsetOverflow) {
				t.Fatalf("New(%d, %d) = %v, want ErrOffsetOverflow", tc.page, tc.pageSize, err)
			}
		})
	}
}

func TestNewAcceptsLargeOffsets(t *testing.T) {
	for _, tc := range []struct {
		name                       string
		page, pageSize, wantOffset int64
	}{
		{"beyond int32", 2_147_483_649, 1, 2_147_483_648},
		{"maximum page at size one", math.MaxInt64, 1, math.MaxInt64 - 1},
		{"exact maximum offset", math.MaxInt64/7 + 1, 7, math.MaxInt64},
		{"last safe page at maximum size", math.MaxInt64/100 + 1, 100, math.MaxInt64 / 100 * 100},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, err := New(tc.page, tc.pageSize)
			if err != nil {
				t.Fatalf("New(%d, %d): %v", tc.page, tc.pageSize, err)
			}
			if got := p.Offset(); got != tc.wantOffset {
				t.Fatalf("offset = %d, want %d", got, tc.wantOffset)
			}
		})
	}
}

func TestNewPageInfoTotalPagesExactAboveFloatPrecision(t *testing.T) {
	// 2^53 + 1 is the smallest integer a float64 cannot represent exactly.
	const totalItems = int64(1<<53 + 1)
	info := NewPageInfo(1, 1, totalItems)
	if got := info.TotalPages(); got != totalItems {
		t.Fatalf("TotalPages() = %d, want %d", got, totalItems)
	}
}

func TestNewPageInfoTotalPages(t *testing.T) {
	for _, tc := range []struct {
		name                            string
		pageSize, totalItems, wantPages int64
	}{
		{"empty result", 20, 0, 0},
		{"exact multiple", 20, 40, 2},
		{"partial last page", 20, 41, 3},
		{"maximum total at size one", 1, math.MaxInt64, math.MaxInt64},
		{"maximum total at size two", 2, math.MaxInt64, math.MaxInt64/2 + 1},
		{"maximum total and size", math.MaxInt64, math.MaxInt64, 1},
		{"zero size", 0, 1, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			info := NewPageInfo(1, tc.pageSize, tc.totalItems)
			if got := info.TotalPages(); got != tc.wantPages {
				t.Fatalf("TotalPages() = %d, want %d", got, tc.wantPages)
			}
		})
	}
}
