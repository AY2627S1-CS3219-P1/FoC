// Package pagination is the single, shared offset-pagination model for the
// project. It owns normalization, offset math and total-page math so every
// offset-paginated endpoint behaves identically without per-service plumbing.
//
// The two types are immutable and valid by construction: build them only
// through New and NewPageInfo. External packages can neither construct them
// outside these constructors nor mutate them, so a Pagination can never hold an
// invalid page or page size and copying is race-free.
package pagination

import (
	"errors"
)

// ErrOffsetOverflow is returned when the requested page and page size map to an
// offset that would exceed the database's offset range. It is a sentinel callers
// compare against to reject a request, never a panic.
var ErrOffsetOverflow = errors.New("pagination offset out of range")

const (
	defaultPage     = 1
	defaultPageSize = 20
	maxPageSize     = 100
	// offsetMaxInt32 is the largest OFFSET PostgreSQL accepts: LIMIT/OFFSET
	// arguments are int4, so a larger offset is rejected by the server. Pages
	// beyond it are rejected here instead of wrapping or erroring in SQL.
	offsetMaxInt32 = 2_147_483_647
)

// Pagination is a request-side offset page. page is the canonical wire field;
// offset is derived and never transmitted alongside page.
type Pagination struct {
	page     int
	pageSize int
}

// New normalizes request input into a Pagination. page and page_size are
// coerced to defaults (page 0 or below -> 1, page_size 0 or below -> 20) and
// page_size is clamped to a maximum of 100. A page whose offset would exceed
// the database's int4 offset range is rejected with ErrOffsetOverflow rather
// than wrapping to a bogus page or failing in SQL.
func New(page, pageSize int) (Pagination, error) {
	p := page
	if p <= 0 {
		p = defaultPage
	}
	size := pageSize
	if size <= 0 {
		size = defaultPageSize
	}
	if size > maxPageSize {
		size = maxPageSize
	}
	// p and size fit in int32 (page comes from an int32 proto field), so the
	// product fits in int64 and cannot overflow.
	if int64(p-1)*int64(size) > offsetMaxInt32 {
		return Pagination{}, ErrOffsetOverflow
	}
	return Pagination{page: p, pageSize: size}, nil
}

// Page returns the effective one-based page number.
func (p Pagination) Page() int { return p.page }

// PageSize returns the effective page size after defaulting and clamping.
func (p Pagination) PageSize() int { return p.pageSize }

// Offset returns the database OFFSET for this page: (page - 1) * page_size.
func (p Pagination) Offset() int { return (p.page - 1) * p.pageSize }

// PageInfo is a response-side holder for the totals of a paginated list. It is
// immutable and built by the service through NewPageInfo; it never carries
// request input, so a request Pagination is never polluted with totals.
type PageInfo struct {
	page       int
	pageSize   int
	totalItems int64
	totalPages int
}

// NewPageInfo builds the response PageInfo for a page of page_size rows whose
// list holds totalItems rows. total_pages is ceil(total_items / page_size),
// and is zero for an empty result. The ceiling is computed in integer math:
// a float64 conversion would lose precision above 2^53.
func NewPageInfo(page, pageSize int, totalItems int64) PageInfo {
	totalPages := 0
	if pageSize > 0 && totalItems > 0 {
		totalPages = int((totalItems + int64(pageSize) - 1) / int64(pageSize))
	}
	return PageInfo{page: page, pageSize: pageSize, totalItems: totalItems, totalPages: totalPages}
}

// Page returns the one-based page returned.
func (p PageInfo) Page() int { return p.page }

// PageSize returns the page size used.
func (p PageInfo) PageSize() int { return p.pageSize }

// TotalItems returns the total number of rows across all pages.
func (p PageInfo) TotalItems() int64 { return p.totalItems }

// TotalPages returns ceil(total_items / page_size), zero for an empty result.
func (p PageInfo) TotalPages() int { return p.totalPages }
