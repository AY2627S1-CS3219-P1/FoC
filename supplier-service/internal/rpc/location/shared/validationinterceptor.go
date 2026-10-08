package shared

import (
	"strings"

	pb "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/supplier/location/v1"
	"google.golang.org/protobuf/proto"
)

// Normalize transport text before declarative length checks. Application
// operations independently enforce the same normalized rules for non-RPC callers.
func normalizeText(message proto.Message) {
	var p *pb.LocationInput
	switch m := message.(type) {
	case *pb.CreateDisablementRequest:
		m.Reason = strings.TrimSpace(m.Reason)
	case *pb.UpdateDisablementRequest:
		m.Reason = strings.TrimSpace(m.Reason)
	case *pb.RejectLocationAdditionRequestRequest:
		m.ReviewNote = strings.TrimSpace(m.ReviewNote)
	case *pb.SubmitLocationAdditionRequestRequest:
		p = m.Proposal
	case *pb.UpdateLocationAdditionRequestRequest:
		p = m.Proposal
	}
	if p == nil {
		return
	}
	p.Name = strings.TrimSpace(p.Name)
	p.Details = strings.TrimSpace(p.Details)
	if p.Floor != nil {
		*p.Floor = strings.TrimSpace(*p.Floor)
	}
	if p.Contact != nil {
		*p.Contact = strings.TrimSpace(*p.Contact)
	}
}
