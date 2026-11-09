package order

import (
	"encoding/json"
	"time"

	db "github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/database/orderdb"
)

// toOrder converts domain values and serializes the local location snapshots.
func toOrder(o Order) (db.Order, error) {
	id, err := parseID(o.ID)
	if err != nil {
		return db.Order{}, err
	}

	requester, err := parseID(o.RequesterID)
	if err != nil {
		return db.Order{}, err
	}

	row := db.Order{
		ID:                   id,
		RequesterID:          requester,
		Status:               string(o.Status),
		DeliveryInstructions: o.DeliveryInstructions,
		ItemDescription:      o.ItemDescription,
		RewardCredits:        o.RewardCredits,
		ReservedCredits:      o.ReservedCredits,
		DeliveryDurationNs:   int64(o.DeliveryDuration),
		ExpiresAt:            o.ExpiresAt,
		AssignedAt:           o.AssignedAt,
		PickedUpAt:           o.PickedUpAt,
		DeliveredAt:          o.DeliveredAt,
		CompletedAt:          o.CompletedAt,
		ClosedAt:             o.ClosedAt,
		CloseReason:          o.CloseReason,
		ItemLocation:         o.ItemLocation,
		UnattendedDelivery:   o.UnattendedDelivery,
		Revision:             o.Revision,
		CreatedAt:            o.CreatedAt,
		UpdatedAt:            o.UpdatedAt,
	}
	if o.CourierID != nil {
		v, e := parseID(*o.CourierID)
		if e != nil {
			return row, e
		}
		row.CourierID = &v
	}

	if o.PendingCancellationID != nil {
		v, err := parseID(*o.PendingCancellationID)
		if err != nil {
			return row, err
		}
		row.PendingCancellationID = &v
	}

	// Normalize snapshot IDs before persisting their independent JSON values.
	pickup, delivery := o.Pickup, o.Delivery
	pickupID, err := parseID(pickup.LocationID)
	if err != nil {
		return row, err
	}
	deliveryID, err := parseID(delivery.LocationID)
	if err != nil {
		return row, err
	}
	pickup.LocationID = pickupID.String()
	delivery.LocationID = deliveryID.String()
	if pickup.LocationID == delivery.LocationID {
		return row, ErrInvalid
	}

	row.Pickup, err = json.Marshal(pickup)
	if err != nil {
		return row, err
	}
	row.Delivery, err = json.Marshal(delivery)
	return row, err
}

// fromOrder restores snapshots and normalizes persisted timestamps to UTC.
func fromOrder(row db.Order) (Order, error) {
	o := Order{
		ID:                   row.ID.String(),
		RequesterID:          row.RequesterID.String(),
		Status:               Status(row.Status),
		DeliveryInstructions: row.DeliveryInstructions,
		ItemDescription:      row.ItemDescription,
		RewardCredits:        row.RewardCredits,
		ReservedCredits:      row.ReservedCredits,
		DeliveryDuration:     time.Duration(row.DeliveryDurationNs),
		ExpiresAt:            row.ExpiresAt.UTC(),
		AssignedAt:           utcTime(row.AssignedAt),
		PickedUpAt:           utcTime(row.PickedUpAt),
		DeliveredAt:          utcTime(row.DeliveredAt),
		CompletedAt:          utcTime(row.CompletedAt),
		ClosedAt:             utcTime(row.ClosedAt),
		CloseReason:          row.CloseReason,
		ItemLocation:         row.ItemLocation,
		UnattendedDelivery:   row.UnattendedDelivery,
		Revision:             row.Revision,
		CreatedAt:            row.CreatedAt.UTC(),
		UpdatedAt:            row.UpdatedAt.UTC(),
	}
	if row.PendingCancellationID != nil {
		v := row.PendingCancellationID.String()
		o.PendingCancellationID = &v
	}
	if row.CourierID != nil {
		v := row.CourierID.String()
		o.CourierID = &v
	}

	if err := json.Unmarshal(row.Pickup, &o.Pickup); err != nil {
		return Order{}, err
	}
	if err := json.Unmarshal(row.Delivery, &o.Delivery); err != nil {
		return Order{}, err
	}

	return o, nil
}

// Offer and timestamp conversion share the same UTC normalization.
func fromOffers(rows []db.Offer) []Offer {
	result := make([]Offer, 0, len(rows))
	for _, r := range rows {
		result = append(result, Offer{
			ID:        r.ID.String(),
			OrderID:   r.OrderID.String(),
			CourierID: r.CourierID.String(),
			Credits:   r.Credits,
			Status:    OfferStatus(r.Status),
			CreatedAt: r.CreatedAt.UTC(),
			UpdatedAt: r.UpdatedAt.UTC(),
		})
	}

	return result
}

func utcTime(v *time.Time) *time.Time {
	if v == nil {
		return nil
	}

	at := v.UTC()
	return &at
}
