package account

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

type fakePayouts struct {
	volumes map[Date]DailyVolume
	from    time.Time
	to      time.Time
}

func (f *fakePayouts) DailyPayouts(_ context.Context, from, to time.Time) (map[Date]DailyVolume, error) {
	f.from, f.to = from, to
	return f.volumes, nil
}

func TestDailyVolume(t *testing.T) {
	ctx := context.Background()
	start, end := Date{2026, time.February, 27}, Date{2026, time.March, 1}
	paid := Date{2026, time.February, 28}
	f := &fakePayouts{volumes: map[Date]DailyVolume{paid: {Date: paid, Credits: 45, Payments: 3}}}
	v := NewVolumes(f)

	invalid := []struct {
		name       string
		start, end Date
	}{
		{"end before start", end, start},
		{"not a calendar day", Date{2026, time.February, 30}, end},
		{"zero date", Date{}, end},
		{"over 366 days", Date{2025, time.January, 1}, Date{2026, time.January, 2}},
	}
	for _, c := range invalid {
		if _, err := v.DailyVolume(ctx, c.start, c.end); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("%s error = %v", c.name, err)
		}
	}
	if !f.from.IsZero() {
		t.Fatal("store called for an invalid request")
	}

	got, err := v.DailyVolume(ctx, start, end)
	if err != nil {
		t.Fatal(err)
	}
	want := []DailyVolume{
		{Date: Date{2026, time.February, 27}},
		{Date: paid, Credits: 45, Payments: 3},
		{Date: Date{2026, time.March, 1}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("days = %v, want %v", got, want)
	}

	// The range is half-open over Singapore midnights.
	from := time.Date(2026, time.February, 26, 16, 0, 0, 0, time.UTC)
	to := time.Date(2026, time.March, 1, 16, 0, 0, 0, time.UTC)
	if !f.from.Equal(from) || !f.to.Equal(to) {
		t.Fatalf("store range = %v to %v", f.from, f.to)
	}
}
