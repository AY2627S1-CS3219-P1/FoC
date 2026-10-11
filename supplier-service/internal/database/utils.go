package database

import (
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

func ToPGText(s string) pgtype.Text {
	return pgtype.Text{
		String: s,
		Valid:  true,
	}
}

func ToPGNullableText(s *string) pgtype.Text {
	if s == nil {
		return pgtype.Text{}
	}
	return pgtype.Text{String: *s, Valid: true}
}

func ToPGTime(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{Valid: false}
	}
	return pgtype.Timestamptz{
		Time:  *t,
		Valid: true,
	}
}

func ToPGTimeOfDay(t *time.Time) pgtype.Time {
	if t == nil {
		return pgtype.Time{}
	}
	microseconds := int64(t.Hour())*time.Hour.Microseconds() +
		int64(t.Minute())*time.Minute.Microseconds() +
		int64(t.Second())*time.Second.Microseconds()
	return pgtype.Time{Microseconds: microseconds, Valid: true}
}

func ToPGDate(t *time.Time) pgtype.Date {
	if t == nil {
		return pgtype.Date{Valid: false}
	}
	return pgtype.Date{
		Time:  *t,
		Valid: true,
	}
}

func ToPGInt4(i *int32) pgtype.Int4 {
	if i == nil {
		return pgtype.Int4{Valid: false}
	}
	return pgtype.Int4{
		Int32: *i,
		Valid: true,
	}
}
