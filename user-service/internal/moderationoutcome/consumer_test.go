package moderationoutcome

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/kafka"
	eventsv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/kafkagen/moderation/events/v1"
)

type applierStub struct {
	got    []Outcome
	result Result
	err    error
}

func (s *applierStub) Apply(_ context.Context, outcome Outcome) (Result, error) {
	s.got = append(s.got, outcome)
	return s.result, s.err
}

func event(action func(*eventsv1.OutcomeRequested)) *eventsv1.OutcomeRequested {
	e := &eventsv1.OutcomeRequested{
		EventId:  uuid.NewString(),
		ActorId:  uuid.NewString(),
		Reason:   "decided",
		ReportId: uuid.NewString(),
	}
	action(e)
	return e
}

func TestConsumerMapsEveryAction(t *testing.T) {
	user, review, warning, request := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	for _, tc := range []struct {
		name   string
		event  *eventsv1.OutcomeRequested
		action Action
		check  func(Outcome) bool
	}{
		{"warn", event(func(e *eventsv1.OutcomeRequested) {
			e.Action = &eventsv1.OutcomeRequested_WarnUser{
				WarnUser: &eventsv1.WarnUser{UserId: user.String(), RequestId: request.String()},
			}
		}), ActionWarnUser, func(o Outcome) bool { return o.UserID == user && o.RequestID == request }},
		{"suspend", event(func(e *eventsv1.OutcomeRequested) {
			e.Action = &eventsv1.OutcomeRequested_SuspendUser{SuspendUser: &eventsv1.SuspendUser{UserId: user.String()}}
		}), ActionSuspendUser, func(o Outcome) bool { return o.UserID == user }},
		{"remove review", event(func(e *eventsv1.OutcomeRequested) {
			e.Action = &eventsv1.OutcomeRequested_RemoveReview{
				RemoveReview: &eventsv1.RemoveReview{ReviewId: review.String()},
			}
		}), ActionRemoveReview, func(o Outcome) bool { return o.ReviewID == review }},
		{"remove warning by id", event(func(e *eventsv1.OutcomeRequested) {
			e.ReportId = ""
			e.Action = &eventsv1.OutcomeRequested_RemoveWarning{RemoveWarning: &eventsv1.RemoveWarning{
				Target: &eventsv1.RemoveWarning_WarningId{WarningId: warning.String()},
			}}
		}), ActionRemoveWarning, func(o Outcome) bool { return o.WarningID == warning }},
		{"restore review", event(func(e *eventsv1.OutcomeRequested) {
			e.Action = &eventsv1.OutcomeRequested_RestoreReview{
				RestoreReview: &eventsv1.RestoreReview{ReviewId: review.String()},
			}
		}), ActionRestoreReview, func(o Outcome) bool { return o.ReviewID == review }},
		{"reinstate", event(func(e *eventsv1.OutcomeRequested) {
			e.Action = &eventsv1.OutcomeRequested_ReinstateUser{
				ReinstateUser: &eventsv1.ReinstateUser{UserId: user.String()},
			}
		}), ActionReinstateUser, func(o Outcome) bool { return o.UserID == user }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stub := &applierStub{result: ResultApplied}
			if err := (&Consumer{Outcomes: stub}).Handle(context.Background(), kafka.Metadata{}, tc.event); err != nil {
				t.Fatal(err)
			}
			if len(stub.got) != 1 || stub.got[0].Action != tc.action || !tc.check(stub.got[0]) {
				t.Fatalf("applied %+v", stub.got)
			}
		})
	}
}

func TestConsumerRemovesAReportWarningThroughItsReport(t *testing.T) {
	report := uuid.New()
	e := event(func(e *eventsv1.OutcomeRequested) {
		e.ReportId = ""
		e.Action = &eventsv1.OutcomeRequested_RemoveWarning{RemoveWarning: &eventsv1.RemoveWarning{
			Target: &eventsv1.RemoveWarning_ByReportId{ByReportId: report.String()},
		}}
	})
	stub := &applierStub{result: ResultApplied}
	if err := (&Consumer{Outcomes: stub}).Handle(context.Background(), kafka.Metadata{}, e); err != nil {
		t.Fatal(err)
	}
	if got := stub.got[0]; got.WarningID != uuid.Nil || got.ReportID != report {
		t.Fatalf("applied %+v", got)
	}
}

func TestConsumerAcknowledgesMalformedEvents(t *testing.T) {
	for name, e := range map[string]*eventsv1.OutcomeRequested{
		"nil":            nil,
		"no action":      event(func(*eventsv1.OutcomeRequested) {}),
		"bad event id":   event(func(e *eventsv1.OutcomeRequested) { e.EventId = "nope" }),
		"warn no report": event(func(e *eventsv1.OutcomeRequested) {
			e.ReportId = ""
			e.Action = &eventsv1.OutcomeRequested_WarnUser{
				WarnUser: &eventsv1.WarnUser{UserId: uuid.NewString(), RequestId: uuid.NewString()},
			}
		}),
	} {
		t.Run(name, func(t *testing.T) {
			stub := &applierStub{}
			if err := (&Consumer{Outcomes: stub}).Handle(context.Background(), kafka.Metadata{}, e); err != nil {
				t.Fatalf("malformed event returned %v, want acknowledged", err)
			}
			if len(stub.got) != 0 {
				t.Fatalf("malformed event applied: %+v", stub.got)
			}
		})
	}
}

func TestConsumerRedeliversTransientFailures(t *testing.T) {
	failure := errors.New("database down")
	e := event(func(e *eventsv1.OutcomeRequested) {
		e.Action = &eventsv1.OutcomeRequested_SuspendUser{SuspendUser: &eventsv1.SuspendUser{UserId: uuid.NewString()}}
	})

	err := (&Consumer{Outcomes: &applierStub{err: failure}}).Handle(context.Background(), kafka.Metadata{}, e)
	if !errors.Is(err, failure) {
		t.Fatalf("transient failure = %v, want it returned", err)
	}

	err = (&Consumer{Outcomes: &applierStub{result: ResultRejected}}).Handle(context.Background(), kafka.Metadata{}, e)
	if err != nil {
		t.Fatalf("rejected outcome = %v, want acknowledged", err)
	}
}
