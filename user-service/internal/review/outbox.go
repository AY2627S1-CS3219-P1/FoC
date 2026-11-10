package review

import (
	"errors"
	"fmt"

	events "github.com/AY2627S1-CS3219-P1/FoC/pkg/kafkagen/users/events/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// Keyed by review UUID as UTF-8 text.
const ReviewSubmittedTopic = "users.review_submitted"

const submittedPayloadVersion = 1

func encodeSubmittedPayload(event *events.ReviewSubmitted) ([]byte, error) {
	if event.GetEventId() == "" || event.GetModerationRequestId() == "" || event.GetReviewId() == "" ||
		event.GetReviewerId() == "" || event.GetContentRevision() < 1 || event.GetComment() == "" ||
		event.GetOccurredAt() == nil {
		return nil, errors.New("incomplete review-submitted event")
	}
	payload, err := protojson.Marshal(event)
	if err != nil {
		return nil, fmt.Errorf("encode review-submitted payload: %w", err)
	}
	return payload, nil
}

func decodeSubmittedPayload(version int32, payload []byte) (proto.Message, error) {
	if version != submittedPayloadVersion {
		return nil, fmt.Errorf("unsupported review-submitted outbox payload version %d", version)
	}
	event := new(events.ReviewSubmitted)
	if err := protojson.Unmarshal(payload, event); err != nil {
		return nil, fmt.Errorf("decode review-submitted outbox payload: %w", err)
	}
	if event.GetEventId() == "" || event.GetModerationRequestId() == "" || event.GetReviewId() == "" ||
		event.GetReviewerId() == "" || event.GetContentRevision() < 1 || event.GetOccurredAt() == nil {
		return nil, errors.New("incomplete review-submitted outbox payload")
	}
	return event, nil
}
