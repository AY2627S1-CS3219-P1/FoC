package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/kafka"
	orders "github.com/AY2627S1-CS3219-P1/FoC/pkg/kafkagen/order/events/v1"
	users "github.com/AY2627S1-CS3219-P1/FoC/pkg/kafkagen/users/events/v1"
	"github.com/jackc/pgx/v5/pgxpool"
	kafkago "github.com/segmentio/kafka-go"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

type eventOptions struct {
	Mode, Corpus   string
	Count          int
	Rate           float64
	Duration, Poll time.Duration
	Out            string
}

type eventInput struct {
	Topic   string          `json:"topic"`
	Key     string          `json:"key"`
	Payload json.RawMessage `json:"payload"`
}

type eventProbe struct {
	eventInput
	message              proto.Message
	id, effect, consumer string
	db                   *pgxpool.Pool
	orderDB              *pgxpool.Pool
	query                string
	args                 []any
}

var eventUUID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

type eventReceipt struct {
	EventID    string     `json:"eventId"`
	Topic      string     `json:"topic"`
	Consumer   string     `json:"consumer"`
	Start      time.Time  `json:"publishStart"`
	Ack        time.Time  `json:"acknowledgedAt"`
	Observed   *time.Time `json:"observedCommitAt,omitempty"`
	LastAbsent *time.Time `json:"lastAbsentAt,omitempty"`
	UpperMS    float64    `json:"latencyUpperMs"`
	Accepted   bool       `json:"accepted"`
	Error      string     `json:"error,omitempty"`
}

func runEvents(ctx context.Context, _ fixtures, options eventOptions) error {
	if options.Count < 1 || options.Rate <= 0 || options.Poll <= 0 || options.Duration <= 0 {
		return errors.New("positive event count, rate, duration and observation interval required")
	}
	var inputs []eventInput
	if err := readJSON(options.Corpus, &inputs); err != nil {
		return err
	}
	if len(inputs) < options.Count {
		return errors.New("event corpus is smaller than count; repeated no-op events cannot establish throughput")
	}
	dbs := map[string]*pgxpool.Pool{}
	for _, name := range []string{"PERF_USER_DB", "PERF_CREDIT_DB", "PERF_COMMUNICATION_DB", "PERF_ORDER_DB"} {
		db, err := connectDB(ctx, name)
		if err != nil {
			return err
		}
		defer db.Close()
		dbs[name] = db
	}
	probes := make([]eventProbe, options.Count)
	seen := map[string]bool{}
	topics := map[string]int{}
	for i, input := range inputs[:options.Count] {
		p, err := newEventProbe(input, dbs)
		if err != nil {
			return fmt.Errorf("corpus index %d: %w", i, err)
		}
		if err := validateEventSource(ctx, p, dbs); err != nil {
			return fmt.Errorf("corpus index %d: %w", i, err)
		}
		if seen[p.id] || seen[p.Topic+":"+p.effect] {
			return errors.New("corpus requires unique event IDs and independent consumer effects")
		}
		seen[p.id] = true
		seen[p.Topic+":"+p.effect] = true
		var already bool
		if err := p.db.QueryRow(ctx, p.query, p.args...).Scan(&already); err != nil {
			return err
		}
		if already {
			return fmt.Errorf("corpus index %d already has its intended effect; throughput would measure a no-op", i)
		}
		probes[i] = p
		topics[p.Topic]++
	}
	publisher, err := kafka.NewPublisher(
		kafka.PublisherConfig{Brokers: strings.Split(os.Getenv("PERF_KAFKA_BROKERS"), ",")},
	)
	if err != nil {
		return err
	}
	defer func() { _ = publisher.Close() }()
	f, err := os.OpenFile(
		filepath.Join(options.Out, options.Mode+"-receipts.jsonl"),
		os.O_CREATE|os.O_EXCL|os.O_WRONLY,
		0600,
	)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	enc := json.NewEncoder(f)
	var mu sync.Mutex
	receipts := make([]eventReceipt, options.Count)
	semaphore := make(chan struct{}, 128)
	var done sync.WaitGroup
	started := time.Now()
	interval := time.Duration(float64(time.Minute) / options.Rate)
	for i, p := range probes {
		due := started.Add(time.Duration(i) * interval)
		if err := waitUntil(ctx, due); err != nil {
			return err
		}
		select {
		case semaphore <- struct{}{}:
		case <-ctx.Done():
			return ctx.Err()
		}
		done.Add(1)
		go func(i int, p eventProbe) {
			defer done.Done()
			defer func() { <-semaphore }()
			r := eventReceipt{EventID: p.id, Topic: p.Topic, Consumer: p.consumer, Start: time.Now().UTC()}
			call, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := publisher.Publish(call, p.Topic, []byte(p.Key), p.message)
			cancel()
			r.Ack = time.Now().UTC()
			r.Accepted = err == nil
			if err != nil {
				r.Error = "publication failed or uncertain"
			}
			err = waitFor(ctx, 30*time.Second, options.Poll, func() (bool, error) {
				var committed bool
				err := p.db.QueryRow(ctx, p.query, p.args...).Scan(&committed)
				observed := time.Now().UTC()
				if committed {
					r.Observed = &observed
					r.UpperMS = float64(observed.Sub(r.Start)) / float64(time.Millisecond)
				} else {
					r.LastAbsent = &observed
				}
				return committed, err
			})
			if err != nil {
				r.Error += "; committed state not reconciled"
			}
			mu.Lock()
			receipts[i] = r
			if err := enc.Encode(r); err != nil {
				receipts[i].Error = "cannot save receipt"
			}
			mu.Unlock()
		}(i, p)
	}
	// Keep the publication window open for the full declared duration even
	// when the final scheduled event starts one interval before its end.
	if options.Mode == "events" {
		if err := waitUntil(ctx, started.Add(options.Duration)); err != nil {
			return err
		}
	}
	done.Wait()
	// Finish initial effects before duplicate checks, so unrelated initial
	// settlements cannot alter the balance snapshots under comparison.
	if options.Mode == "replay" {
		for i, p := range probes {
			if receipts[i].Observed == nil {
				continue
			}
			if err := checkReplay(ctx, publisher, p, options.Out); err != nil {
				receipts[i].Error += "; " + err.Error()
			}
		}
		if err := saveJSON(filepath.Join(options.Out, "replay-reconciliation.json"), receipts); err != nil {
			return err
		}
	}
	accepted, within, reconciled := 0, 0, 0
	clean := true
	var first, last time.Time
	for _, r := range receipts {
		if first.IsZero() || r.Start.Before(first) {
			first = r.Start
		}
		if r.Start.After(last) {
			last = r.Start
		}
		if r.Accepted {
			accepted++
			if r.Observed != nil {
				reconciled++
				if r.UpperMS <= 5000 {
					within++
				}
			}
		}
		clean = clean && r.Error == ""
	}
	accountIDs := map[string]bool{}
	for _, p := range probes {
		switch e := p.message.(type) {
		case *users.UserCreated:
			accountIDs[e.UserId] = true
		case *orders.OrderStatusChanged:
			accountIDs[e.RequesterId] = true
		case *orders.OrderOutcome:
			accountIDs[e.RequesterId] = true
			if e.GetCourierId() != "" {
				accountIDs[e.GetCourierId()] = true
			}
		case *orders.CourierWarningRequested:
			accountIDs[e.CourierId] = true
		}
	}
	ids := make([]string, 0, len(accountIDs))
	for id := range accountIDs {
		ids = append(ids, id)
	}
	var inconsistent, accounts int
	if err := dbs["PERF_CREDIT_DB"].QueryRow(ctx, financialConsistencySQL, ids).
		Scan(&inconsistent, &accounts); err != nil {
		return err
	}
	passed := accepted > 0 && float64(within)/float64(accepted) >= 0.99 && reconciled == accepted &&
		inconsistent == 0 &&
		accounts == len(ids)
	if options.Mode == "events" {
		passed = passed && options.Count >= 10000 && options.Duration >= 10*time.Minute && options.Rate >= 1000 &&
			last.Sub(
				first,
			) >= 10*time.Minute-2*interval && len(topics) == 4 && publicationCadence(receipts, started, options.Duration)
	} else {
		passed = passed && clean && len(topics) == 4
	}
	return recordResult(options.Out, options.Mode, map[string]any{
		"accepted":                 accepted,
		"withinFiveSeconds":        within,
		"reconciled":               reconciled,
		"topics":                   topics,
		"pollMs":                   options.Poll.Milliseconds(),
		"publicationWindowSeconds": options.Duration.Seconds(),
		"timing":                   "publish-start to observed committed DB effect; conservative upper bound; lastAbsentAt includes DB read uncertainty",
		"clock":                    "single generator clock; no broker/application timestamp subtraction",
		"allReceiptsClean":         clean,
		"inconsistentAccounts":     inconsistent,
		"reconciledAccounts":       accounts,
	}, passed)
}

func publicationCadence(receipts []eventReceipt, start time.Time, duration time.Duration) bool {
	buckets := make([]int, int(duration/time.Minute))
	for _, r := range receipts {
		bucket := int(r.Start.Sub(start) / time.Minute)
		if r.Accepted && bucket >= 0 && bucket < len(buckets) {
			buckets[bucket]++
		}
	}
	for _, count := range buckets {
		if count < 1000 {
			return false
		}
	}
	return len(buckets) >= 10
}

func waitUntil(ctx context.Context, at time.Time) error {
	timer := time.NewTimer(time.Until(at))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func newEventProbe(input eventInput, dbs map[string]*pgxpool.Pool) (eventProbe, error) {
	p := eventProbe{eventInput: input, orderDB: dbs["PERF_ORDER_DB"]}
	switch input.Topic {
	case "users.create":
		p.message = new(users.UserCreated)
	case "order.status_changed":
		p.message = new(orders.OrderStatusChanged)
	case "order.outcome":
		p.message = new(orders.OrderOutcome)
	case "order.courier_warning_requested":
		p.message = new(orders.CourierWarningRequested)
	default:
		return p, errors.New("unknown deployed event topic")
	}
	if err := protojson.Unmarshal(input.Payload, p.message); err != nil {
		return p, err
	}
	switch e := p.message.(type) {
	case *users.UserCreated:
		p.id, p.effect, p.consumer = e.EventId, e.UserId, "credit-service"
		p.db = dbs["PERF_CREDIT_DB"]
		p.query = `SELECT EXISTS(SELECT 1 FROM accounts a WHERE user_id=$1 AND balance>0 AND
 (SELECT count(*) FROM ledger_entries WHERE user_id=a.user_id AND kind='GRANT')=1)`
		p.args = []any{e.UserId}
	case *orders.OrderStatusChanged:
		p.id, p.effect, p.consumer = e.EventId, e.OrderId, "communication-service"
		p.db = dbs["PERF_COMMUNICATION_DB"]
		p.query = `SELECT EXISTS(SELECT 1 FROM order_participants WHERE order_id=$1 AND revision=$2 AND requester_id=$3 AND status=$4 AND coalesce(courier_id::text,'')=$5)`
		p.args = []any{
			e.OrderId,
			e.Revision,
			e.RequesterId,
			strings.ToLower(strings.TrimPrefix(e.Status.String(), "ORDER_STATUS_")),
			e.GetCourierId(),
		}
	case *orders.OrderOutcome:
		p.id, p.effect, p.consumer = e.EventId, e.OrderId, "credit-service"
		p.db = dbs["PERF_CREDIT_DB"]
		status := "RELEASED"
		if e.Outcome == orders.OrderOutcomeType_ORDER_OUTCOME_TYPE_COMPLETED ||
			e.Outcome == orders.OrderOutcomeType_ORDER_OUTCOME_TYPE_CANCELLED_AFTER_PICKUP {
			status = "SETTLED"
		}
		paid := int64(0)
		outcome := "RELEASED"
		if status == "SETTLED" {
			paid = e.ReservedCredits
			outcome = strings.TrimPrefix(e.Outcome.String(), "ORDER_OUTCOME_TYPE_")
			if e.Outcome == orders.OrderOutcomeType_ORDER_OUTCOME_TYPE_COMPLETED {
				if e.DeliveryDuration.CheckValid() != nil || e.DeliveryDuration.AsDuration() <= 0 ||
					e.PickedUpAt.CheckValid() != nil ||
					e.DeliveredAt.CheckValid() != nil {
					return p, errors.New("invalid completion times")
				}
				allowed := e.DeliveryDuration.AsDuration().Truncate(time.Microsecond)
				elapsed := e.DeliveredAt.AsTime().
					Truncate(time.Microsecond).
					Sub(e.PickedUpAt.AsTime().Truncate(time.Microsecond))
				if elapsed < 0 {
					return p, errors.New("delivery precedes pickup")
				}
				if elapsed >= 2*allowed {
					paid = 0
				} else if elapsed > allowed {
					n := new(big.Int).Mul(big.NewInt(paid), big.NewInt(int64(2*allowed-elapsed)))
					paid = n.Quo(n, big.NewInt(int64(allowed))).Int64()
				}
			}
		}
		p.query = `SELECT EXISTS(SELECT 1 FROM reservations WHERE order_id=$1 AND requester_id=$2 AND status=$3
 AND reserved_amount=0 AND settled_at IS NOT NULL AND settlement_outcome=$4 AND paid_to_courier=$5 AND returned_to_requester=$6)`
		p.args = []any{e.OrderId, e.RequesterId, status, outcome, paid, e.ReservedCredits - paid}
	case *orders.CourierWarningRequested:
		p.id, p.effect, p.consumer = e.EventId, e.OrderId, "user-service"
		p.db = dbs["PERF_USER_DB"]
		p.query = `SELECT (SELECT count(*) FROM account_warnings WHERE source_event_id=$1 AND user_id=$2 AND request_id=$3)=1`
		p.args = []any{e.EventId, e.CourierId, e.OrderId}
	}
	if !eventUUID.MatchString(p.id) || !eventUUID.MatchString(p.effect) || input.Key != p.effect {
		return p, errors.New("event identity and topic key must match the contract")
	}
	return p, nil
}

func validateEventSource(ctx context.Context, p eventProbe, dbs map[string]*pgxpool.Pool) error {
	var valid bool
	var err error
	switch e := p.message.(type) {
	case *users.UserCreated:
		if e.CreatedAt.CheckValid() != nil {
			return errors.New("invalid creation timestamp")
		}
		err = dbs["PERF_USER_DB"].QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id=$1 AND email=$2 AND display_name=$3 AND deleted_at IS NULL)`, e.UserId, e.Email, e.DisplayName).
			Scan(&valid)
	case *orders.OrderStatusChanged:
		if e.OccurredAt.CheckValid() != nil || e.Revision < 2 ||
			e.Status == orders.OrderStatus_ORDER_STATUS_UNSPECIFIED {
			return errors.New("invalid status revision or timestamp")
		}
		err = dbs["PERF_ORDER_DB"].QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM orders WHERE id=$1 AND requester_id=$2 AND revision=$3 AND status=$4 AND coalesce(courier_id::text,'')=$5)`, e.OrderId, e.RequesterId, e.Revision, strings.ToLower(strings.TrimPrefix(e.Status.String(), "ORDER_STATUS_")), e.GetCourierId()).
			Scan(&valid)
	case *orders.OrderOutcome:
		if e.OccurredAt.CheckValid() != nil || e.ReservedCredits <= 0 {
			return errors.New("invalid terminal amount or timestamp")
		}
		status := map[orders.OrderOutcomeType]string{
			orders.OrderOutcomeType_ORDER_OUTCOME_TYPE_COMPLETED:               "completed",
			orders.OrderOutcomeType_ORDER_OUTCOME_TYPE_CANCELLED_BEFORE_PICKUP: "cancelled",
			orders.OrderOutcomeType_ORDER_OUTCOME_TYPE_CANCELLED_AFTER_PICKUP:  "cancelled",
			orders.OrderOutcomeType_ORDER_OUTCOME_TYPE_EXPIRED:                 "expired",
			orders.OrderOutcomeType_ORDER_OUTCOME_TYPE_FAILED:                  "failed",
		}[e.Outcome]
		if status == "" {
			return errors.New("unsupported outcome")
		}
		err = dbs["PERF_ORDER_DB"].QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM orders WHERE id=$1 AND requester_id=$2 AND status=$3)`, e.OrderId, e.RequesterId, status).
			Scan(&valid)
		if err == nil && valid {
			err = dbs["PERF_CREDIT_DB"].QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM reservations WHERE order_id=$1 AND requester_id=$2 AND reserved_amount=$3 AND status IN ('HELD','ASSIGNED'))`, e.OrderId, e.RequesterId, e.ReservedCredits).
				Scan(&valid)
		}
		if err == nil && valid &&
			(e.Outcome == orders.OrderOutcomeType_ORDER_OUTCOME_TYPE_COMPLETED || e.Outcome == orders.OrderOutcomeType_ORDER_OUTCOME_TYPE_CANCELLED_AFTER_PICKUP) {
			err = dbs["PERF_CREDIT_DB"].QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM reservations r JOIN assignments a ON a.order_id=r.order_id AND a.assignment_id=r.assignment_id WHERE r.order_id=$1 AND r.status='ASSIGNED' AND a.courier_id=$2 AND a.offer_amount=$3 AND a.status='APPLIED')`, e.OrderId, e.GetCourierId(), e.ReservedCredits).
				Scan(&valid)
		}
	case *orders.CourierWarningRequested:
		if e.OccurredAt.CheckValid() != nil ||
			(e.Reason != orders.CourierWarningReason_COURIER_WARNING_REASON_CANCELLED_AFTER_PICKUP && e.Reason != orders.CourierWarningReason_COURIER_WARNING_REASON_ZERO_PAYABLE_REWARD) {
			return errors.New("invalid warning reason or timestamp")
		}
		err = dbs["PERF_ORDER_DB"].QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM orders WHERE id=$1 AND courier_id=$2 AND status='failed')`, e.OrderId, e.CourierId).
			Scan(&valid)
	}
	if err != nil {
		return err
	}
	if !valid {
		return errors.New("event does not match its staged source state")
	}
	return nil
}

func checkReplay(ctx context.Context, publisher *kafka.Publisher, p eventProbe, out string) error {
	before, err := consumerSnapshot(ctx, p)
	if err != nil {
		return err
	}
	var group sync.WaitGroup
	errorsCh := make(chan error, 10)
	for i := 0; i < 10; i++ {
		group.Add(1)
		go func() { defer group.Done(); errorsCh <- publisher.Publish(ctx, p.Topic, []byte(p.Key), p.message) }()
	}
	group.Wait()
	close(errorsCh)
	for err := range errorsCh {
		if err != nil {
			return errors.New("duplicate publication failed")
		}
	}
	variant := proto.Clone(p.message)
	switch e := variant.(type) {
	case *orders.OrderOutcome:
		e.EventId = fixtureID(0, "terminal-replay-"+p.id, 0)
	case *orders.OrderStatusChanged:
		if e.Revision > 1 {
			e.Revision--
			e.EventId = fixtureID(0, "old-revision-"+p.id, 0)
		}
	}
	if err := publisher.Publish(ctx, p.Topic, []byte(p.Key), variant); err != nil {
		return errors.New("variant publication failed")
	}
	// A committed offset beyond the post-publication high water confirms all
	// duplicates were handled. Correctness is still asserted against the DB.
	if err := waitForConsumer(ctx, p, out); err != nil {
		return err
	}
	after, err := consumerSnapshot(ctx, p)
	if err != nil {
		return err
	}
	if err := saveJSON(filepath.Join(out, "replay-state-"+p.id+".json"), map[string]any{
		"eventId":   p.id,
		"topic":     p.Topic,
		"consumer":  p.consumer,
		"before":    json.RawMessage(before),
		"after":     json.RawMessage(after),
		"unchanged": before == after,
	}); err != nil {
		return err
	}
	if before != after {
		return errors.New("duplicate or older revision changed committed effects")
	}
	return nil
}

func waitForConsumer(ctx context.Context, p eventProbe, out string) error {
	brokers := strings.Split(os.Getenv("PERF_KAFKA_BROKERS"), ",")
	conn, err := kafkago.DialContext(ctx, "tcp", brokers[0])
	if err != nil {
		return errors.New("cannot inspect duplicate consumption")
	}
	defer func() { _ = conn.Close() }()
	partitions, err := conn.ReadPartitions(p.Topic)
	if err != nil {
		return err
	}
	requests := []kafkago.OffsetRequest{}
	indices := []int{}
	for _, partition := range partitions {
		requests = append(requests, kafkago.LastOffsetOf(partition.ID))
		indices = append(indices, partition.ID)
	}
	client := &kafkago.Client{Addr: kafkago.TCP(brokers...), Timeout: 5 * time.Second}
	high, err := client.ListOffsets(
		ctx,
		&kafkago.ListOffsetsRequest{Topics: map[string][]kafkago.OffsetRequest{p.Topic: requests}},
	)
	if err != nil {
		return err
	}
	target := map[int]int64{}
	for _, partition := range high.Topics[p.Topic] {
		if partition.Error != nil {
			return partition.Error
		}
		target[partition.Partition] = partition.LastOffset
	}
	if len(target) != len(indices) || len(target) == 0 {
		return errors.New("missing topic high-water evidence")
	}
	committed := map[int]int64{}
	err = waitFor(ctx, 30*time.Second, 100*time.Millisecond, func() (bool, error) {
		offsets, err := client.OffsetFetch(
			ctx,
			&kafkago.OffsetFetchRequest{GroupID: p.consumer, Topics: map[string][]int{p.Topic: indices}},
		)
		if err != nil {
			return false, err
		}
		if offsets.Error != nil {
			return false, offsets.Error
		}
		caught := 0
		for _, offset := range target {
			if offset == 0 {
				caught++
			}
		}
		for _, partition := range offsets.Topics[p.Topic] {
			if partition.Error != nil {
				return false, partition.Error
			}
			if target[partition.Partition] > 0 && partition.CommittedOffset >= target[partition.Partition] {
				caught++
			}
			committed[partition.Partition] = partition.CommittedOffset
		}
		return caught == len(target), nil
	})
	if err != nil {
		return err
	}
	return saveJSON(filepath.Join(out, "replay-offsets-"+p.id+".json"), map[string]any{
		"eventId":                  p.id,
		"topic":                    p.Topic,
		"consumer":                 p.consumer,
		"postPublicationHighWater": target,
		"committed":                committed,
		"observedAt":               time.Now().UTC(),
	})
}

func consumerSnapshot(ctx context.Context, p eventProbe) (string, error) {
	var query string
	switch p.Topic {
	case "users.create":
		query = `SELECT jsonb_build_object('account',(SELECT to_jsonb(a) FROM accounts a WHERE user_id=$1),'ledger',(SELECT jsonb_agg(to_jsonb(l) ORDER BY id) FROM ledger_entries l WHERE user_id=$1))::text`
	case "order.outcome":
		query = `SELECT jsonb_build_object('reservation',(SELECT to_jsonb(r) FROM reservations r WHERE order_id=$1),'ledger',(SELECT jsonb_agg(to_jsonb(l) ORDER BY id) FROM ledger_entries l WHERE order_id=$1),'accounts',(SELECT jsonb_agg(to_jsonb(a) ORDER BY user_id) FROM accounts a WHERE user_id IN (SELECT requester_id FROM reservations WHERE order_id=$1 UNION SELECT courier_id FROM assignments WHERE order_id=$1)))::text`
	case "order.status_changed":
		query = `SELECT jsonb_build_object('projection',(SELECT to_jsonb(p) FROM order_participants p WHERE order_id=$1),'notifications',(SELECT jsonb_agg(to_jsonb(n) ORDER BY event_id,user_id) FROM order_notifications_sent n WHERE event_id=ANY($2::uuid[])))::text`
	case "order.courier_warning_requested":
		query = `SELECT coalesce(jsonb_agg(to_jsonb(w) ORDER BY id),'[]')::text FROM account_warnings w WHERE source_event_id=$1`
	}
	args := []any{p.effect}
	if p.Topic == "order.status_changed" {
		args = append(args, []string{p.id, fixtureID(0, "old-revision-"+p.id, 0)})
	}
	if p.Topic == "order.courier_warning_requested" {
		args = []any{p.id}
	}
	var snapshot string
	err := p.db.QueryRow(ctx, query, args...).Scan(&snapshot)
	if err != nil {
		return "", err
	}
	if p.Topic == "users.create" {
		return snapshot, nil
	}
	var source string
	err = p.orderDB.QueryRow(ctx, `SELECT jsonb_build_object('order',to_jsonb(o),'audit',
 (SELECT jsonb_agg(to_jsonb(a) ORDER BY revision) FROM order_audit a WHERE order_id=o.id))::text FROM orders o WHERE id=$1`, p.effect).Scan(&source)
	if err != nil {
		return "", err
	}
	combined, err := json.Marshal(
		map[string]json.RawMessage{"source": json.RawMessage(source), "consumer": json.RawMessage(snapshot)},
	)
	return string(combined), err
}
