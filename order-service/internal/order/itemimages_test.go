package order

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/images"
)

type imageViewRepository struct {
	ItemImageRepository
	calls int
}

func (r *imageViewRepository) OrderImages(context.Context, string) ([]ItemImageUpload, error) {
	r.calls++
	return []ItemImageUpload{{ID: "image", Key: "private-image"}}, nil
}

type imageViewStorage struct {
	images.ObjectStorage
	calls int
}

func (s *imageViewStorage) CreateDownload(context.Context, string) (images.DownloadGrant, error) {
	s.calls++
	return images.DownloadGrant{URL: "signed-download"}, nil
}

func TestItemImagesForOrderVisibility(t *testing.T) {
	courier := "courier"
	for _, tc := range []struct {
		name   string
		caller Caller
		want   bool
	}{
		{name: "requester", caller: Caller{ID: "requester"}, want: true},
		{name: "assigned courier", caller: Caller{ID: courier}, want: true},
		{name: "admin", caller: Caller{ID: "admin", Admin: true}, want: true},
		{name: "suspended participant", caller: Caller{ID: "requester", Suspended: true}, want: true},
		{name: "unrelated courier", caller: Caller{ID: "other"}},
		{name: "suspended nonparticipant", caller: Caller{ID: "other", Suspended: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repository := &imageViewRepository{}
			storage := &imageViewStorage{}
			service := NewItemImages(repository, storage)
			for _, status := range []Status{AwaitingCourier, AwaitingPickup, Completed} {
				for _, expiry := range []time.Time{time.Now().Add(time.Hour), time.Now().Add(-time.Hour)} {
					o := Order{
						ID:          "order",
						RequesterID: "requester",
						CourierID:   &courier,
						Status:      status,
						ExpiresAt:   expiry,
					}
					views, err := service.ForOrder(context.Background(), tc.caller, o)
					if err != nil || (len(views) == 1) != tc.want {
						t.Fatalf("status %s: views=%v err=%v", status, views, err)
					}
				}
			}
			if !tc.want && (repository.calls != 0 || storage.calls != 0) {
				t.Fatal("nonparticipant accessed private image storage")
			}
		})
	}
}

func TestItemImagesForOrderRejectsAnonymous(t *testing.T) {
	_, err := NewItemImages(nil, nil).ForOrder(context.Background(), Caller{}, Order{})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("got %v, want forbidden", err)
	}
}
