package account

import (
	"context"
	"fmt"
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

// DailyVolume is the credits paid to couriers on one day and the number of
// payments. A settlement that paid the courier nothing is not a payment.
type DailyVolume struct {
	Date     Date
	Credits  int64
	Payments int64
}

// PayoutStore reads courier payouts in [from, to) for each day that has any.
type PayoutStore interface {
	DailyPayouts(ctx context.Context, from, to time.Time) (map[Date]DailyVolume, error)
}

type Volumes struct{ store PayoutStore }

func NewVolumes(store PayoutStore) *Volumes { return &Volumes{store: store} }

// DailyVolume returns the volume for each day from start to end inclusive.
// Every day in the range is present.
func (v *Volumes) DailyVolume(ctx context.Context, start, end Date) ([]DailyVolume, error) {
	if !start.IsValid() || !end.IsValid() {
		return nil, ErrInvalidArgument
	}

	// Fixed-offset days are exactly 24 hours, so the range divides evenly.
	from, to := start.midnight(), end.midnight().AddDate(0, 0, 1)
	days := int(to.Sub(from) / (24 * time.Hour))
	if days < 1 || days > MaxDailyRange {
		return nil, ErrInvalidArgument
	}

	payouts, err := v.store.DailyPayouts(ctx, from, to)
	if err != nil {
		return nil, fmt.Errorf("daily payouts: %w", err)
	}

	out := make([]DailyVolume, days)
	for i := range out {
		day := dateOf(from.AddDate(0, 0, i))
		out[i] = payouts[day]
		out[i].Date = day
	}
	return out, nil
}
