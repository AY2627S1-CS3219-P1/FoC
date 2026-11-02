// Package pagination is the shared offset-pagination model. It owns
// normalization, offset and total-page math so every offset-paginated
// endpoint behaves identically.
//
// Both types are immutable and valid by construction: build them only
// through New and NewPageInfo.
package pagination

import (
	"errors"
	"math"
)

// ErrOffsetOverflow is returned when a page's offset cannot fit in int64.
var ErrOffsetOverflow = errors.New("pagination offset out of range")

const (
	defaultPage     = 1
	defaultPageSize = 20
	maxPageSize     = 100
)

// Pagination is a request-side offset page.
type Pagination struct {
	page     int64
	pageSize int64
}

func New(page, pageSize int64) (Pagination, error) {
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
	// Check before multiplying: overflow can wrap to a nonnegative offset.
	if p-1 > math.MaxInt64/size {
		return Pagination{}, ErrOffsetOverflow
	}
	return Pagination{page: p, pageSize: size}, nil
}

func (p Pagination) Page() int64 { return p.page }

func (p Pagination) PageSize() int64 { return p.pageSize }

func (p Pagination) Offset() int64 { return (p.page - 1) * p.pageSize }

// PageInfo is a response-side holder for the totals of a paginated list.
type PageInfo struct {
	page       int64
	pageSize   int64
	totalItems int64
	totalPages int64
}

func NewPageInfo(page, pageSize, totalItems int64) PageInfo {
	var totalPages int64
	if pageSize > 0 && totalItems > 0 {
		totalPages = (totalItems-1)/pageSize + 1
	}
	return PageInfo{page: page, pageSize: pageSize, totalItems: totalItems, totalPages: totalPages}
}

func (p PageInfo) Page() int64 { return p.page }

func (p PageInfo) PageSize() int64 { return p.pageSize }

func (p PageInfo) TotalItems() int64 { return p.totalItems }

func (p PageInfo) TotalPages() int64 { return p.totalPages }
