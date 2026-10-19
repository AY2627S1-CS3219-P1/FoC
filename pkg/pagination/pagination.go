// Package pagination is the shared offset-pagination model. It owns
// normalization, offset and total-page math so every offset-paginated
// endpoint behaves identically.
//
// Both types are immutable and valid by construction: build them only
// through New and NewPageInfo.
package pagination

import (
	"errors"
)

// ErrOffsetOverflow is returned when a page's offset exceeds the database's
// offset range.
var ErrOffsetOverflow = errors.New("pagination offset out of range")

const (
	defaultPage     = 1
	defaultPageSize = 20
	maxPageSize     = 100
	// PostgreSQL OFFSET arguments are int4; a larger offset is rejected
	// here instead of failing in SQL.
	offsetMaxInt32 = 2_147_483_647
)

// Pagination is a request-side offset page.
type Pagination struct {
	page     int
	pageSize int
}

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
	// page comes from an int32 proto field, so the product fits in int64.
	if int64(p-1)*int64(size) > offsetMaxInt32 {
		return Pagination{}, ErrOffsetOverflow
	}
	return Pagination{page: p, pageSize: size}, nil
}

func (p Pagination) Page() int { return p.page }

func (p Pagination) PageSize() int { return p.pageSize }

func (p Pagination) Offset() int { return (p.page - 1) * p.pageSize }

// PageInfo is a response-side holder for the totals of a paginated list.
type PageInfo struct {
	page       int
	pageSize   int
	totalItems int64
	totalPages int
}

func NewPageInfo(page, pageSize int, totalItems int64) PageInfo {
	totalPages := 0
	if pageSize > 0 && totalItems > 0 {
		totalPages = int((totalItems + int64(pageSize) - 1) / int64(pageSize))
	}
	return PageInfo{page: page, pageSize: pageSize, totalItems: totalItems, totalPages: totalPages}
}

func (p PageInfo) Page() int { return p.page }

func (p PageInfo) PageSize() int { return p.pageSize }

func (p PageInfo) TotalItems() int64 { return p.totalItems }

func (p PageInfo) TotalPages() int { return p.totalPages }
