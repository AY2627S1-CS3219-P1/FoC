package rpc

import (
	"time"

	pb "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/supplier/location/v1"
	app "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/location/lifecycle"
	"google.golang.org/genproto/googleapis/type/timeofday"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func timestamp(t *time.Time) *timestamppb.Timestamp {
	if t == nil {
		return nil
	}
	return timestamppb.New(t.UTC())
}

func absolute(t *timestamppb.Timestamp) *time.Time {
	if t == nil {
		return nil
	}
	v := t.AsTime().UTC()
	return &v
}

func wallTime(t *timeofday.TimeOfDay) *int64 {
	if t == nil {
		return nil
	}
	v := int64(t.Hours)*3600000000 + int64(t.Minutes)*60000000 + int64(t.Seconds)*1000000 + int64(t.Nanos)/1000
	return &v
}

func wireWall(t *int64) *timeofday.TimeOfDay {
	if t == nil {
		return nil
	}
	n := *t
	return &timeofday.TimeOfDay{Hours: int32(n / 3600000000), Minutes: int32(n / 60000000 % 60), Seconds: int32(n / 1000000 % 60), Nanos: int32(n%1000000) * 1000}
}

func proposal(p *pb.LocationInput) app.Proposal {
	if p == nil {
		return app.Proposal{}
	}
	return app.Proposal{Name: p.Name, IsSupplier: p.GetIsSupplier(), CategoryIDs: p.CategoryIds, BuildingID: p.BuildingId, Floor: p.Floor, Latitude: p.GetCoordinates().GetLatitude(), Longitude: p.GetCoordinates().GetLongitude(), OpenFrom: wallTime(p.OpensAt), OpenTo: wallTime(p.ClosesAt), Contact: p.Contact, Details: p.Details}
}

func toProtoLocationInput(p app.Proposal) *pb.LocationInput {
	classification := p.IsSupplier
	out := &pb.LocationInput{Name: p.Name, IsSupplier: &classification, CategoryIds: p.CategoryIDs, BuildingId: p.BuildingID, Floor: p.Floor, Coordinates: &pb.Coordinates{Latitude: p.Latitude, Longitude: p.Longitude}, OpensAt: wireWall(p.OpenFrom), ClosesAt: wireWall(p.OpenTo), Contact: p.Contact, Details: p.Details}
	if p.CoordinatesMissing {
		out.Coordinates = nil
	}
	return out
}

var stateFromWire = map[pb.DisablementState]app.DisablementState{pb.DisablementState_DISABLEMENT_STATE_UNSPECIFIED: "", pb.DisablementState_DISABLEMENT_STATE_SCHEDULED: app.Scheduled, pb.DisablementState_DISABLEMENT_STATE_ACTIVE: app.Active, pb.DisablementState_DISABLEMENT_STATE_ENDED: app.Ended, pb.DisablementState_DISABLEMENT_STATE_CANCELLED: app.Cancelled}

var stateToWire = map[app.DisablementState]pb.DisablementState{app.Scheduled: pb.DisablementState_DISABLEMENT_STATE_SCHEDULED, app.Active: pb.DisablementState_DISABLEMENT_STATE_ACTIVE, app.Ended: pb.DisablementState_DISABLEMENT_STATE_ENDED, app.Cancelled: pb.DisablementState_DISABLEMENT_STATE_CANCELLED}

var statusFromWire = map[pb.LocationAdditionRequestStatus]app.RequestStatus{pb.LocationAdditionRequestStatus_LOCATION_ADDITION_REQUEST_STATUS_UNSPECIFIED: "", pb.LocationAdditionRequestStatus_LOCATION_ADDITION_REQUEST_STATUS_PENDING: app.Pending, pb.LocationAdditionRequestStatus_LOCATION_ADDITION_REQUEST_STATUS_APPROVED: app.Approved, pb.LocationAdditionRequestStatus_LOCATION_ADDITION_REQUEST_STATUS_REJECTED: app.Rejected, pb.LocationAdditionRequestStatus_LOCATION_ADDITION_REQUEST_STATUS_WITHDRAWN: app.Withdrawn}

var statusToWire = map[app.RequestStatus]pb.LocationAdditionRequestStatus{app.Pending: pb.LocationAdditionRequestStatus_LOCATION_ADDITION_REQUEST_STATUS_PENDING, app.Approved: pb.LocationAdditionRequestStatus_LOCATION_ADDITION_REQUEST_STATUS_APPROVED, app.Rejected: pb.LocationAdditionRequestStatus_LOCATION_ADDITION_REQUEST_STATUS_REJECTED, app.Withdrawn: pb.LocationAdditionRequestStatus_LOCATION_ADDITION_REQUEST_STATUS_WITHDRAWN}

func (h *WorkflowServer) toProtoDisablement(d app.Disablement) *pb.Disablement {
	return &pb.Disablement{Id: d.ID, LocationId: d.LocationID, StartsAt: timestamp(&d.StartsAt), EndsAt: timestamp(d.EndsAt), EndedAt: timestamp(d.EndedAt), CancelledAt: timestamp(d.CancelledAt), Reason: d.Reason, CreatedBy: d.CreatedBy, State: stateToWire[d.State(h.clock())], Revision: d.Revision, CreatedAt: timestamp(&d.CreatedAt), UpdatedAt: timestamp(&d.UpdatedAt)}
}

func toProtoAdditionRequest(r app.AdditionRequest) *pb.LocationAdditionRequest {
	return &pb.LocationAdditionRequest{Id: r.ID, Proposal: toProtoLocationInput(r.Proposal), SubmittedBy: r.SubmittedBy, Status: statusToWire[r.Status], ReviewedBy: r.ReviewedBy, ReviewedAt: timestamp(r.ReviewedAt), ReviewNote: r.ReviewNote, ResultingLocationId: r.ResultingLocationID, Revision: r.Revision, CreatedAt: timestamp(&r.CreatedAt), UpdatedAt: timestamp(&r.UpdatedAt)}
}

func (h *WorkflowServer) toProtoWorkflowLocation(l app.Location) *pb.Location {
	p := l.Proposal
	out := &pb.Location{Id: l.ID, Name: p.Name, IsSupplier: p.IsSupplier, Building: &pb.Building{Id: l.Building.ID, Name: l.Building.Name, Center: &pb.Coordinates{Latitude: l.Building.Latitude, Longitude: l.Building.Longitude}, RadiusM: l.Building.RadiusM, CreatedAt: timestamp(&l.Building.CreatedAt), UpdatedAt: timestamp(&l.Building.UpdatedAt)}, Floor: p.Floor, Coordinates: &pb.Coordinates{Latitude: p.Latitude, Longitude: p.Longitude}, OpensAt: wireWall(p.OpenFrom), ClosesAt: wireWall(p.OpenTo), Contact: p.Contact, Details: p.Details, ArchivedAt: timestamp(l.ArchivedAt), Revision: l.Revision, CreatedAt: timestamp(&l.CreatedAt), UpdatedAt: timestamp(&l.UpdatedAt)}
	for _, c := range l.Categories {
		out.Categories = append(out.Categories, &pb.Category{Id: c.ID, Name: c.Name, CreatedAt: timestamp(&c.CreatedAt)})
	}
	if l.CurrentDisablement != nil {
		out.CurrentDisablement = h.toProtoDisablement(*l.CurrentDisablement)
	}
	return out
}

func toProtoPageInfo(p app.PageInfo) *pb.PageInfo {
	return &pb.PageInfo{Page: p.Number, PageSize: p.Size, TotalItems: p.TotalItems, TotalPages: p.TotalPages}
}

// Translate API opening-hour names without changing the internal domain fields.
func proposalPaths(paths []string) []string {
	out := append([]string(nil), paths...)
	for i, path := range out {
		switch path {
		case "opens_at":
			out[i] = "open_from"
		case "closes_at":
			out[i] = "open_to"
		}
	}
	return out
}
