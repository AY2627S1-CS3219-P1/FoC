package additionrequest

import (
	pb "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/supplier/location/v1"
	app "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/location/additionrequest"
	rpcshared "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/rpc/location/shared"
)

func proposal(p *pb.LocationInput) app.Proposal {
	if p == nil {
		return app.Proposal{}
	}
	return app.Proposal{Name: p.Name, IsSupplier: p.GetIsSupplier(), CategoryIDs: p.CategoryIds, BuildingID: p.BuildingId, Floor: p.Floor, Latitude: p.GetCoordinates().GetLatitude(), Longitude: p.GetCoordinates().GetLongitude(), OpenFrom: rpcshared.WallTime(p.OpensAt), OpenTo: rpcshared.WallTime(p.ClosesAt), Contact: p.Contact, Details: p.Details}
}

func toProtoLocationInput(p app.Proposal) *pb.LocationInput {
	classification := p.IsSupplier
	out := &pb.LocationInput{Name: p.Name, IsSupplier: &classification, CategoryIds: p.CategoryIDs, BuildingId: p.BuildingID, Floor: p.Floor, Coordinates: &pb.Coordinates{Latitude: p.Latitude, Longitude: p.Longitude}, OpensAt: rpcshared.WireWall(p.OpenFrom), ClosesAt: rpcshared.WireWall(p.OpenTo), Contact: p.Contact, Details: p.Details}
	if p.CoordinatesMissing {
		out.Coordinates = nil
	}
	return out
}

var statusFromWire = map[pb.LocationAdditionRequestStatus]app.RequestStatus{pb.LocationAdditionRequestStatus_LOCATION_ADDITION_REQUEST_STATUS_UNSPECIFIED: "", pb.LocationAdditionRequestStatus_LOCATION_ADDITION_REQUEST_STATUS_PENDING: app.Pending, pb.LocationAdditionRequestStatus_LOCATION_ADDITION_REQUEST_STATUS_APPROVED: app.Approved, pb.LocationAdditionRequestStatus_LOCATION_ADDITION_REQUEST_STATUS_REJECTED: app.Rejected, pb.LocationAdditionRequestStatus_LOCATION_ADDITION_REQUEST_STATUS_WITHDRAWN: app.Withdrawn}

var statusToWire = map[app.RequestStatus]pb.LocationAdditionRequestStatus{app.Pending: pb.LocationAdditionRequestStatus_LOCATION_ADDITION_REQUEST_STATUS_PENDING, app.Approved: pb.LocationAdditionRequestStatus_LOCATION_ADDITION_REQUEST_STATUS_APPROVED, app.Rejected: pb.LocationAdditionRequestStatus_LOCATION_ADDITION_REQUEST_STATUS_REJECTED, app.Withdrawn: pb.LocationAdditionRequestStatus_LOCATION_ADDITION_REQUEST_STATUS_WITHDRAWN}

func toProtoAdditionRequest(r app.AdditionRequest) *pb.LocationAdditionRequest {
	return &pb.LocationAdditionRequest{Id: r.ID, Proposal: toProtoLocationInput(r.Proposal), SubmittedBy: r.SubmittedBy, Status: statusToWire[r.Status], ReviewedBy: r.ReviewedBy, ReviewedAt: rpcshared.Timestamp(r.ReviewedAt), ReviewNote: r.ReviewNote, ResultingLocationId: r.ResultingLocationID, Revision: r.Revision, CreatedAt: rpcshared.Timestamp(&r.CreatedAt), UpdatedAt: rpcshared.Timestamp(&r.UpdatedAt)}
}

func (h *Server) toProtoLocation(l app.Location) *pb.Location {
	p := l.Proposal
	out := &pb.Location{Id: l.ID, Name: p.Name, IsSupplier: p.IsSupplier, Building: &pb.Building{Id: l.Building.ID, Name: l.Building.Name, Center: &pb.Coordinates{Latitude: l.Building.Latitude, Longitude: l.Building.Longitude}, RadiusM: l.Building.RadiusM, CreatedAt: rpcshared.Timestamp(&l.Building.CreatedAt), UpdatedAt: rpcshared.Timestamp(&l.Building.UpdatedAt)}, Floor: p.Floor, Coordinates: &pb.Coordinates{Latitude: p.Latitude, Longitude: p.Longitude}, OpensAt: rpcshared.WireWall(p.OpenFrom), ClosesAt: rpcshared.WireWall(p.OpenTo), Contact: p.Contact, Details: p.Details, ArchivedAt: rpcshared.Timestamp(l.ArchivedAt), Revision: l.Revision, CreatedAt: rpcshared.Timestamp(&l.CreatedAt), UpdatedAt: rpcshared.Timestamp(&l.UpdatedAt)}
	for _, c := range l.Categories {
		out.Categories = append(out.Categories, &pb.Category{Id: c.ID, Name: c.Name, CreatedAt: rpcshared.Timestamp(&c.CreatedAt)})
	}
	if l.CurrentDisablement != nil {
		out.CurrentDisablement = rpcshared.Disablement(*l.CurrentDisablement, h.clock())
	}
	return out
}

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
