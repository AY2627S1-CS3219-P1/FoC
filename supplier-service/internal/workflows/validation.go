package workflows

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

func validID(id string) error {
	if _, err := uuid.Parse(id); err != nil {
		return ErrInvalidArgument
	}
	return nil
}

func trimLimit(s string, min, max int) (string, error) {
	s = strings.TrimSpace(s)
	n := utf8.RuneCountInString(s)
	if n < min || n > max {
		return "", ErrInvalidArgument
	}
	return s, nil
}

func normalizeTime(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	v := t.UTC()
	return &v
}

func pagination(p Page) (Page, error) {
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

func pageInfo(p Page, count int64) PageInfo {
	pages := (count + int64(p.Size) - 1) / int64(p.Size)
	if pages > math.MaxInt32 {
		pages = math.MaxInt32
	}
	return PageInfo{Page: p, TotalItems: count, TotalPages: int32(pages)}
}

func mask(paths []string, allowed ...string) (map[string]bool, error) {
	if len(paths) == 0 {
		return nil, ErrInvalidArgument
	}
	m := map[string]bool{}
	for _, p := range paths {
		ok := false
		for _, a := range allowed {
			if p == a {
				ok = true
			}
		}
		if !ok || m[p] {
			return nil, ErrInvalidArgument
		}
		m[p] = true
	}
	return m, nil
}
