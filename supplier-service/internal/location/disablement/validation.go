package disablement

import (
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

func requireAuthenticated(c Caller) error {
	if !c.Authenticated() {
		return ErrUnauthenticated
	}
	return nil
}

func requireAdmin(c Caller) error {
	if err := requireAuthenticated(c); err != nil {
		return err
	}
	if !c.IsAdmin() {
		return ErrPermissionDenied
	}
	return nil
}

func validateID(id string) error {
	if _, err := uuid.Parse(id); err != nil {
		return ErrInvalidArgument
	}
	return nil
}

func trimAndValidateLength(s string, min, max int) (string, error) {
	s = strings.TrimSpace(s)
	n := utf8.RuneCountInString(s)
	if n < min || n > max {
		return "", ErrInvalidArgument
	}
	return s, nil
}

func toUTC(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	v := t.UTC()
	return &v
}

func normalizePage(p Page) (Page, error) {
	if p.Number < 0 || p.Size < 0 || p.Size > 100 {
		return p, ErrInvalidArgument
	}
	if p.Number == 0 {
		p.Number = 1
	}
	if p.Size == 0 {
		p.Size = 20
	}
	return p, nil
}

func newPageInfo(p Page, count int64) PageInfo {
	pages := (count + int64(p.Size) - 1) / int64(p.Size)
	if pages > math.MaxInt32 {
		pages = math.MaxInt32
	}
	return PageInfo{Page: p, TotalItems: count, TotalPages: int32(pages)}
}

func parseFieldMask(paths []string, allowedPaths ...string) (map[string]bool, error) {
	if len(paths) == 0 {
		return nil, ErrInvalidArgument
	}
	fields := map[string]bool{}
	for _, path := range paths {
		allowed := false
		for _, allowedPath := range allowedPaths {
			if path == allowedPath {
				allowed = true
			}
		}
		if !allowed || fields[path] {
			return nil, ErrInvalidArgument
		}
		fields[path] = true
	}
	return fields, nil
}
