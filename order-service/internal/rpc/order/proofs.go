package order

import (
	"context"

	"connectrpc.com/connect"
	"github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/images"
	app "github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/order"
	"github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/rpc/shared"
	pb "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/order/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type ProofOperations interface {
	CreateUpload(context.Context, app.Caller, app.ProofUploadInput) (app.ProofUpload, images.UploadGrant, error)
	VerifyUpload(context.Context, app.Caller, string, string) error
	AbandonUpload(context.Context, app.Caller, string, string) error
	ForOrder(context.Context, app.Caller, string) (app.DeliveryProofView, error)
}

type ProofReceiptOperations interface {
	ConfirmDelivery(context.Context, app.Caller, app.ConfirmDeliveryInput) (app.Order, error)
}

func (s *Server) WithProofs(proofs ProofOperations, receipts ProofReceiptOperations) *Server {
	s.proofs = proofs
	s.proofReceipts = receipts
	return s
}

func (s *Server) CreateProofUpload(
	ctx context.Context,
	req *connect.Request[pb.CreateProofUploadRequest],
) (*connect.Response[pb.CreateProofUploadResponse], error) {
	if s.proofs == nil {
		return nil, shared.ConnectError(ctx, app.ErrUnavailable)
	}
	m := req.Msg
	row, grant, err := s.proofs.CreateUpload(
		ctx,
		shared.Caller(ctx),
		app.ProofUploadInput{
			OrderID:     m.OrderId,
			UploadID:    m.UploadId,
			ContentType: m.ContentType,
			SizeBytes:   m.SizeBytes,
		},
	)
	if err != nil {
		return nil, shared.ConnectError(ctx, err)
	}
	return connect.NewResponse(
		&pb.CreateProofUploadResponse{
			ProofId:       row.ID,
			UploadUrl:     grant.URL,
			UploadHeaders: grant.Headers,
			ExpiresAt:     timestamppb.New(grant.ExpiresAt),
		},
	), nil
}

func (s *Server) VerifyProofUpload(
	ctx context.Context,
	req *connect.Request[pb.VerifyProofUploadRequest],
) (*connect.Response[pb.VerifyProofUploadResponse], error) {
	if s.proofs == nil {
		return nil, shared.ConnectError(ctx, app.ErrUnavailable)
	}
	if err := s.proofs.VerifyUpload(ctx, shared.Caller(ctx), req.Msg.OrderId, req.Msg.ProofId); err != nil {
		return nil, shared.ConnectError(ctx, err)
	}
	return connect.NewResponse(&pb.VerifyProofUploadResponse{}), nil
}

func (s *Server) AbandonProofUpload(
	ctx context.Context,
	req *connect.Request[pb.AbandonProofUploadRequest],
) (*connect.Response[pb.AbandonProofUploadResponse], error) {
	if s.proofs == nil {
		return nil, shared.ConnectError(ctx, app.ErrUnavailable)
	}
	if err := s.proofs.AbandonUpload(ctx, shared.Caller(ctx), req.Msg.OrderId, req.Msg.UploadId); err != nil {
		return nil, shared.ConnectError(ctx, err)
	}
	return connect.NewResponse(&pb.AbandonProofUploadResponse{}), nil
}

func (s *Server) ConfirmDelivery(
	ctx context.Context,
	req *connect.Request[pb.ConfirmDeliveryRequest],
) (*connect.Response[pb.ConfirmDeliveryResponse], error) {
	if s.proofReceipts == nil {
		return nil, shared.ConnectError(ctx, app.ErrUnavailable)
	}
	caller := shared.Caller(ctx)
	m := req.Msg
	result, err := s.proofReceipts.ConfirmDelivery(
		ctx,
		caller,
		app.ConfirmDeliveryInput{
			OrderID:      m.OrderId,
			ProofIDs:     m.ProofIds,
			Unattended:   m.Unattended,
			WithoutProof: m.WithoutProof,
		},
	)
	if err != nil {
		return nil, shared.ConnectError(ctx, err)
	}
	view, err := shared.OrderView(ctx, caller, result, s.images)
	if err != nil {
		return nil, shared.ConnectError(ctx, err)
	}
	return connect.NewResponse(&pb.ConfirmDeliveryResponse{Order: view}), nil
}

func (s *Server) GetDeliveryProof(
	ctx context.Context,
	req *connect.Request[pb.GetDeliveryProofRequest],
) (*connect.Response[pb.GetDeliveryProofResponse], error) {
	if s.proofs == nil {
		return nil, shared.ConnectError(ctx, app.ErrUnavailable)
	}
	result, err := s.proofs.ForOrder(ctx, shared.Caller(ctx), req.Msg.OrderId)
	if err != nil {
		return nil, shared.ConnectError(ctx, err)
	}
	response := &pb.GetDeliveryProofResponse{OutageReason: result.OutageReason}
	for _, photo := range result.Proofs {
		response.Proofs = append(response.Proofs, &pb.DeliveryProofPhoto{
			ProofId:     photo.ID,
			DownloadUrl: photo.DownloadURL,
			ContentType: photo.ContentType,
			ExpiresAt:   timestamppb.New(photo.ExpiresAt),
		})
	}
	if r := result.Pending; r != nil {
		response.Pending = &pb.PendingDeliveryConfirmation{
			ProofIds:     r.ProofIDs,
			Unattended:   r.Unattended,
			WithoutProof: r.WithoutProof,
			ReceivedAt:   timestamppb.New(r.ReceivedAt),
		}
	}
	return connect.NewResponse(response), nil
}
