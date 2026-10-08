package disablement

import (
	"context"
	"time"

	"github.com/google/uuid"
)

func validateDisablement(d *Disablement) error {
	var err error
	d.Reason, err = trimAndValidateLength(d.Reason, 1, 500)
	if err != nil {
		return err
	}
	d.StartsAt = d.StartsAt.UTC()
	d.EndsAt = toUTC(d.EndsAt)
	if d.EndsAt != nil && !d.EndsAt.After(d.StartsAt) {
		return ErrInvalidArgument
	}
	return nil
}

func (s *Service) CreateDisablement(ctx context.Context, c Caller, in CreateDisablement) (out Disablement, err error) {
	if err = requireAdmin(c); err != nil {
		return out, err
	}
	if err = validateID(in.LocationID); err != nil {
		return out, err
	}
	in.LocationID = uuid.MustParse(in.LocationID).String()
	// Hash the explicit input, not the clock-derived default start. A retry with
	// omitted start must remain identical when wall time has advanced.
	in.Reason, err = trimAndValidateLength(in.Reason, 1, 500)
	if err != nil {
		return out, err
	}
	in.StartsAt = toUTC(in.StartsAt)
	in.EndsAt = toUTC(in.EndsAt)
	payload := struct {
		LocationID       string
		StartsAt, EndsAt *time.Time
		Reason           string
	}{in.LocationID, in.StartsAt, in.EndsAt, in.Reason}
	err = s.repo.Within(ctx, func(tx Tx) error {
		id, e := s.idempotent(ctx, tx, c, "CreateDisablement", in.Key, payload, func(now time.Time) (string, error) {
			l, e := tx.Location(ctx, in.LocationID)
			if e != nil {
				return "", e
			}
			if l.ArchivedAt != nil {
				return "", ErrFailedPrecondition
			}
			d := Disablement{ID: uuid.NewString(), LocationID: in.LocationID, StartsAt: now, EndsAt: in.EndsAt, Reason: in.Reason, CreatedBy: c.ID, Revision: 1, CreatedAt: now, UpdatedAt: now}
			if in.StartsAt != nil {
				d.StartsAt = *in.StartsAt
			}
			if e = validateDisablement(&d); e != nil {
				return "", e
			}
			overlap, e := tx.Overlaps(ctx, d)
			if e != nil {
				return "", e
			}
			if overlap {
				return "", ErrAlreadyExists
			}
			return d.ID, tx.SaveDisablement(ctx, d, 0)
		})
		if e != nil {
			return e
		}
		out, e = tx.Disablement(ctx, id)
		return e
	})
	return out, err
}

func (s *Service) ListDisablements(ctx context.Context, c Caller, locationID string, state DisablementState, p Page) (out DisablementPage, err error) {
	if err = requireAdmin(c); err != nil {
		return out, err
	}
	if err = validateID(locationID); err != nil {
		return out, err
	}
	if state != "" && state != Scheduled && state != Active && state != Ended && state != Cancelled {
		return out, ErrInvalidArgument
	}
	p, err = normalizePage(p)
	if err != nil {
		return out, err
	}
	err = s.repo.Within(ctx, func(tx Tx) error {
		if _, e := tx.Location(ctx, locationID); e != nil {
			return e
		}
		items, n, e := tx.ListDisablements(ctx, locationID, state, s.clock().UTC(), p)
		out = DisablementPage{Items: items, PageInfo: newPageInfo(p, n)}
		return e
	})
	return out, err
}

func (s *Service) UpdateDisablement(ctx context.Context, c Caller, in UpdateDisablement) (out Disablement, err error) {
	if err = requireAdmin(c); err != nil {
		return out, err
	}
	if validateID(in.ID) != nil || in.ExpectedRevision <= 0 {
		return out, ErrInvalidArgument
	}
	m, e := parseFieldMask(in.Paths, "starts_at", "ends_at", "reason")
	if e != nil {
		return out, e
	}
	err = s.repo.Within(ctx, func(tx Tx) error {
		d, e := tx.Disablement(ctx, in.ID)
		if e != nil {
			return e
		}
		if _, e = tx.Location(ctx, d.LocationID); e != nil {
			return e
		}
		now := s.clock().UTC()
		if d.State(now) != Scheduled {
			return ErrFailedPrecondition
		}
		if d.Revision != in.ExpectedRevision {
			return ErrAborted
		}
		if m["starts_at"] {
			if in.StartsAt == nil {
				return ErrInvalidArgument
			}
			d.StartsAt = *in.StartsAt
		}
		if m["ends_at"] {
			d.EndsAt = in.EndsAt
		}
		if m["reason"] {
			d.Reason = in.Reason
		}
		if e = validateDisablement(&d); e != nil {
			return e
		}
		overlap, e := tx.Overlaps(ctx, d)
		if e != nil {
			return e
		}
		if overlap {
			return ErrAlreadyExists
		}
		d.Revision++
		d.UpdatedAt = now
		if e = tx.SaveDisablement(ctx, d, in.ExpectedRevision); e != nil {
			return e
		}
		out, e = tx.Disablement(ctx, d.ID)
		return e
	})
	return out, err
}

func (s *Service) EndDisablement(ctx context.Context, c Caller, id string) (Disablement, error) {
	return s.transitionDisablement(ctx, c, id, Ended)
}

func (s *Service) CancelDisablement(ctx context.Context, c Caller, id string) (Disablement, error) {
	return s.transitionDisablement(ctx, c, id, Cancelled)
}

func (s *Service) transitionDisablement(ctx context.Context, c Caller, id string, target DisablementState) (out Disablement, err error) {
	if err = requireAdmin(c); err != nil {
		return out, err
	}
	if err = validateID(id); err != nil {
		return out, err
	}
	err = s.repo.Within(ctx, func(tx Tx) error {
		d, e := tx.Disablement(ctx, id)
		if e != nil {
			return e
		}
		now := s.clock().UTC()
		state := d.State(now)
		if state == target {
			out = d
			return nil
		}
		if (target == Ended && state != Active) || (target == Cancelled && state != Scheduled) {
			return ErrFailedPrecondition
		}
		prev := d.Revision
		if target == Ended {
			d.EndedAt = &now
		} else {
			d.CancelledAt = &now
		}
		d.Revision++
		d.UpdatedAt = now
		if e = tx.SaveDisablement(ctx, d, prev); e != nil {
			return e
		}
		out, e = tx.Disablement(ctx, d.ID)
		return e
	})
	return out, err
}
