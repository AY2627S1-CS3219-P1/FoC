package discovery

import (
	"fmt"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api/errs"
)

func normalizeList(caller Caller, req ListRequest) (Query, int32, int32, error) {
	search := strings.TrimSpace(req.Search)
	if length := utf8.RuneCountInString(search); length > MaxSearchLength {
		return Query{}, 0, 0, errs.NewBadRequestError(
			fmt.Sprintf("search has %d characters; maximum is %d", length, MaxSearchLength))
	}
	if req.Page < 0 {
		return Query{}, 0, 0, errs.NewBadRequestError(
			fmt.Sprintf("page is %d; it cannot be negative", req.Page))
	}
	if req.PageSize < 0 {
		return Query{}, 0, 0, errs.NewBadRequestError(
			fmt.Sprintf("page_size is %d; it cannot be negative", req.PageSize))
	}
	if req.PageSize > MaxPageSize {
		return Query{}, 0, 0, errs.NewBadRequestError(
			fmt.Sprintf("page_size is %d; maximum is %d", req.PageSize, MaxPageSize))
	}

	archive := req.Archive
	switch archive {
	case "":
		archive = ArchiveActive
	case ArchiveActive:
	case ArchiveArchived, ArchiveAll:
		if !caller.Admin {
			return Query{}, 0, 0, ErrPermissionDenied
		}
	default:
		return Query{}, 0, 0, errs.NewBadRequestError(
			fmt.Sprintf("archive value %q is invalid; use active, archived, or all", archive))
	}

	sort := req.Sort
	switch sort {
	case "":
		sort = SortByName
	case SortByName, SortByBuilding:
	default:
		return Query{}, 0, 0, errs.NewBadRequestError(
			fmt.Sprintf("sort value %q is invalid; use name or building", sort))
	}

	page := max(req.Page, 1)
	pageSize := req.PageSize
	if pageSize == 0 {
		pageSize = DefaultPageSize
	}
	if int64(page-1)*int64(pageSize) > math.MaxInt32 {
		return Query{}, 0, 0, errs.NewBadRequestError(
			fmt.Sprintf("page %d with page_size %d exceeds the supported offset range", page, pageSize))
	}

	return Query{
		Search:        search,
		BuildingID:    req.BuildingID,
		CategoryID:    req.CategoryID,
		SuppliersOnly: req.SuppliersOnly,
		Archive:       archive,
		Sort:          sort,
		Descending:    req.Descending,
		Offset:        (page - 1) * pageSize,
		Limit:         pageSize,
	}, page, pageSize, nil
}
