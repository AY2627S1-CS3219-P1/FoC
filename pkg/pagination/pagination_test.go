package pagination

import (
	"errors"
	"testing"
)

func TestNewDefaults(t *testing.T) {
	for _, tc := range []struct {
		name           string
		page, pageSize int
		wantPage       int
		wantPageSize   int
		wantOffset     int
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
				t.Fatalf("New(%d, %d) = page %d size %d offset %d, want page %d size %d offset %d",
					tc.page, tc.pageSize, p.Page(), p.PageSize(), p.Offset(), tc.wantPage, tc.wantPageSize, tc.wantOffset)
			}
		})
	}
}

func TestNewRejectsOffsetBeyondDatabaseLimit(t *testing.T) {
	for _, tc := range []struct {
		name           string
		page, pageSize int
	}{
		{"page beyond the int4 offset range", 2_147_483_649, 1},
		{"large page at the default size", 107_374_184, 20},
		{"large page at the maximum size", 21_474_838, 100},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := New(tc.page, tc.pageSize)
			if !errors.Is(err, ErrOffsetOverflow) {
				t.Fatalf("New(%d, %d) = %v, want ErrOffsetOverflow", tc.page, tc.pageSize, err)
			}
		})
	}
}

func TestNewAcceptsOffsetAtDatabaseLimit(t *testing.T) {
	// (page-1)*size == offsetMaxInt32 is the largest offset PostgreSQL accepts.
	p, err := New(1+offsetMaxInt32/100, 100)
	if err != nil {
		t.Fatalf("New at the offset boundary: %v", err)
	}
	if got := p.Offset(); got != offsetMaxInt32/100*100 {
		t.Fatalf("offset = %d, want %d", got, offsetMaxInt32/100*100)
	}
}

func TestNewPageInfoTotalPagesExactAboveFloatPrecision(t *testing.T) {
	// 2^53 + 1 is the smallest integer a float64 cannot represent exactly; the
	// old math.Ceil(float64/float64) computation would return 2^53 here.
	const totalItems = int64(1<<53 + 1)
	info := NewPageInfo(1, 1, totalItems)
	if got := info.TotalPages(); got != int(totalItems) {
		t.Fatalf("TotalPages() = %d, want %d", got, totalItems)
	}
}
