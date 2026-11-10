package order

import (
	"context"
	"time"
)

// Daily series use Asia/Singapore calendar days. Singapore has no daylight
// saving, so a fixed offset avoids depending on the container's tzdata.
var singapore = time.FixedZone("Asia/Singapore", 8*60*60)

// MaxDailyRange bounds a daily series to about a year of days.
const MaxDailyRange = 366

// Date is an Asia/Singapore calendar day.
type Date struct {
	Year  int
	Month time.Month
	Day   int
}

// IsValid reports whether the date names a real calendar day.
func (d Date) IsValid() bool {
	t := d.midnight()
	return d.Year >= 1 && t.Year() == d.Year && t.Month() == d.Month && t.Day() == d.Day
}

func (d Date) midnight() time.Time {
	return time.Date(d.Year, d.Month, d.Day, 0, 0, 0, 0, singapore)
}

func dateOf(t time.Time) Date {
	t = t.In(singapore)
	return Date{Year: t.Year(), Month: t.Month(), Day: t.Day()}
}

// DailyCount is the number of orders created on one day.
type DailyCount struct {
	Date  Date
	Count int64
}

// CountOrdersByDay counts orders created on each day from start to end
// inclusive, for administrators. Every day in the range is present.
func (s *Service) CountOrdersByDay(ctx context.Context, c Caller, start, end Date) ([]DailyCount, error) {
	if !c.Admin {
		return nil, ErrForbidden
	}
	if !start.IsValid() || !end.IsValid() {
		return nil, ErrInvalid
	}

	// Fixed-offset days are exactly 24 hours, so the range divides evenly.
	from, to := start.midnight(), end.midnight().AddDate(0, 0, 1)
	days := int(to.Sub(from) / (24 * time.Hour))
	if days < 1 || days > MaxDailyRange {
		return nil, ErrInvalid
	}

	counts, err := s.reader.CountByDay(ctx, from, to)
	if err != nil {
		return nil, err
	}

	out := make([]DailyCount, days)
	for i := range out {
		day := dateOf(from.AddDate(0, 0, i))
		out[i] = DailyCount{Date: day, Count: counts[day]}
	}
	return out, nil
}
