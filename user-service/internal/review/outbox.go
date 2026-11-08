// Review submission outbox: the ReviewSubmitted payload stored with the review
// row and published to users.review_submitted after the commit.
package review

import (
	"encoding/json"
	"errors"
	"fmt"

	events "github.com/AY2627S1-CS3219-P1/FoC/pkg/kafkagen/users/events/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// ReviewSubmittedTopic carries committed pending reviews to the content
// moderation service, keyed by the review UUID as UTF-8 text.
const ReviewSubmittedTopic = "users.review_submitted"

// submittedPayloadVersion pins the outbox payload schema; the column check
// rejects anything else.
const submittedPayloadVersion = 1

// encodeSubmittedPayload renders the event as the JSON stored in the outbox
// row. It rejects incomplete events so a malformed payload can never commit.
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

// decodeSubmittedPayload rebuilds the event for publication and validates the
// payload version and completeness before the relay touches the broker.
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

// submittedReasonCodes decodes the stored reason_codes JSON of an unpublished
// review; an empty array means no codes.
func submittedReasonCodes(raw []byte) ([]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var codes []string
	if err := json.Unmarshal(raw, &codes); err != nil {
		return nil, fmt.Errorf("decode review reason codes: %w", err)
	}
	return codes, nil
}

// submittedReasonCodesJSON renders the reason codes for storage; approval and
// errors store an empty array, never null.
func submittedReasonCodesJSON(codes []string) (json.RawMessage, error) {
	if codes == nil {
		codes = []string{}
	}
	raw, err := json.Marshal(codes)
	if err != nil {
		return nil, fmt.Errorf("encode review reason codes: %w", err)
	}
	return raw, nil
}
