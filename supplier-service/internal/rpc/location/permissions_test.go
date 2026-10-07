package location_test

import (
	"connectrpc.com/connect"
	"context"
	pb "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/supplier/location/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"testing"
)

func TestLocationRPCPermissions(t *testing.T) {
	client, _ := fastClients(t, &applicationStub{})
	const id = "9dbda827-5a46-4089-9209-a2bde43f18a0"
	for _, role := range []string{"", "user", "suspended_user", "admin", "super_admin"} {
		roleName := role
		if roleName == "" {
			roleName = "unauthenticated"
		}
		t.Run(roleName, func(t *testing.T) {
			// The test mounts generated discovery/admin stubs to verify that
			// authentication and authorization run before dispatch.
			for _, read := range []func() error{
				func() error {
					_, err := client.GetLocation(context.Background(), fastRequest(&pb.GetLocationRequest{Id: id}, role))
					return err
				},
				func() error {
					_, err := client.ListLocations(context.Background(), fastRequest(&pb.ListLocationsRequest{}, role))
					return err
				},
				func() error {
					_, err := client.ListBuildings(context.Background(), fastRequest(&pb.ListBuildingsRequest{}, role))
					return err
				},
				func() error {
					_, err := client.ListCategories(context.Background(), fastRequest(&pb.ListCategoriesRequest{}, role))
					return err
				},
			} {
				want := connect.CodeUnimplemented
				if role == "" {
					want = connect.CodeUnauthenticated
				}
				if err := read(); connect.CodeOf(err) != want {
					t.Fatalf("read as %q: %v, want %v", role, err, want)
				}
			}
			for _, mutate := range []func() error{
				func() error {
					_, err := client.CreateLocation(context.Background(), fastRequest(&pb.CreateLocationRequest{IdempotencyKey: id, Location: &pb.LocationInput{Name: "Library", IsSupplier: proto.Bool(false), BuildingId: id, Coordinates: &pb.Coordinates{Latitude: 1.294, Longitude: 103.774}}}, role))
					return err
				},
				func() error {
					_, err := client.UpdateLocation(context.Background(), fastRequest(&pb.UpdateLocationRequest{Id: id, ExpectedRevision: 1, Location: &pb.LocationInput{Floor: proto.String("2")}, UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"floor"}}}, role))
					return err
				},
				func() error {
					_, err := client.ArchiveLocation(context.Background(), fastRequest(&pb.ArchiveLocationRequest{Id: id}, role))
					return err
				},
				func() error {
					_, err := client.UnarchiveLocation(context.Background(), fastRequest(&pb.UnarchiveLocationRequest{Id: id}, role))
					return err
				},
			} {
				want := connect.CodePermissionDenied
				if role == "" {
					want = connect.CodeUnauthenticated
				} else if role == "admin" || role == "super_admin" {
					want = connect.CodeUnimplemented
				}
				if err := mutate(); connect.CodeOf(err) != want {
					t.Fatalf("mutation as %q: %v, want %v", role, err, want)
				}
			}
		})
	}
}
