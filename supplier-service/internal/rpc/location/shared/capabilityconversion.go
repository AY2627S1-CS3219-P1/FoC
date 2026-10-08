package shared

import (
	"time"

	pb "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/supplier/location/v1"
	app "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/location/shared"
	"google.golang.org/genproto/googleapis/type/timeofday"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func Timestamp(t *time.Time) *timestamppb.Timestamp {
	if t == nil {
		return nil
	}
	return timestamppb.New(t.UTC())
}

func Absolute(t *timestamppb.Timestamp) *time.Time {
	if t == nil {
		return nil
	}
	v := t.AsTime().UTC()
	return &v
}

func WallTime(t *timeofday.TimeOfDay) *int64 {
	if t == nil {
		return nil
	}
	v := int64(t.Hours)*3600000000 + int64(t.Minutes)*60000000 + int64(t.Seconds)*1000000 + int64(t.Nanos)/1000
	return &v
}

func WireWall(t *int64) *timeofday.TimeOfDay {
	if t == nil {
		return nil
	}
	n := *t
	return &timeofday.TimeOfDay{Hours: int32(n / 3600000000), Minutes: int32(n / 60000000 % 60), Seconds: int32(n / 1000000 % 60), Nanos: int32(n%1000000) * 1000}
}

func PageInfo(p app.PageInfo) *pb.PageInfo {
	return &pb.PageInfo{Page: p.Number, PageSize: p.Size, TotalItems: p.TotalItems, TotalPages: p.TotalPages}
}

var stateToWire = map[app.DisablementState]pb.DisablementState{app.Scheduled: pb.DisablementState_DISABLEMENT_STATE_SCHEDULED, app.Active: pb.DisablementState_DISABLEMENT_STATE_ACTIVE, app.Ended: pb.DisablementState_DISABLEMENT_STATE_ENDED, app.Cancelled: pb.DisablementState_DISABLEMENT_STATE_CANCELLED}

func Disablement(d app.Disablement, now time.Time) *pb.Disablement {
	return &pb.Disablement{Id: d.ID, LocationId: d.LocationID, StartsAt: Timestamp(&d.StartsAt), EndsAt: Timestamp(d.EndsAt), EndedAt: Timestamp(d.EndedAt), CancelledAt: Timestamp(d.CancelledAt), Reason: d.Reason, CreatedBy: d.CreatedBy, State: stateToWire[d.State(now)], Revision: d.Revision, CreatedAt: Timestamp(&d.CreatedAt), UpdatedAt: Timestamp(&d.UpdatedAt)}
}
