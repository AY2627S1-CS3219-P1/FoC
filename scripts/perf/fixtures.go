package main

import (
	"crypto/md5" // Deterministic fixture identity, never a credential.
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	orders "github.com/AY2627S1-CS3219-P1/FoC/pkg/kafkagen/order/events/v1"
	users "github.com/AY2627S1-CS3219-P1/FoC/pkg/kafkagen/users/events/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type fixtureSpec struct {
	Seed      int       `json:"seed"`
	Users     int       `json:"users"`
	Suppliers int       `json:"suppliers"`
	Open      int       `json:"open"`
	History   int       `json:"history"`
	At        time.Time `json:"at"`
}

type fixtures struct {
	Spec        fixtureSpec `json:"spec"`
	UserIDs     []string    `json:"userIds"`
	LocationIDs []string    `json:"locationIds"`
	OpenIDs     []string    `json:"openIds"`
	HistoryIDs  []string    `json:"historyIds"`
	Files       []string    `json:"files"`
}

func fixtureID(seed int, kind string, index int) string {
	digest := md5.Sum(fmt.Appendf(nil, "foc-perf:%d:%s:%d", seed, kind, index))
	// Custom deterministic UUIDv8 with the RFC variant. Raw MD5 bytes can
	// violate browser UUID validation even though PostgreSQL accepts them.
	digest[6] = digest[6]&0x0f | 0x80
	digest[8] = digest[8]&0x3f | 0x80
	h := hex.EncodeToString(digest[:])
	return h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}

func readFixtures(path string) (fixtures, error) {
	var m fixtures
	if err := readJSON(path, &m); err != nil {
		return m, err
	}
	if m.Spec.Users < 51 || m.Spec.Suppliers < 2 || m.Spec.Open < 1 || m.Spec.History < 1 ||
		len(m.UserIDs) != m.Spec.Users || len(m.LocationIDs) != m.Spec.Suppliers ||
		len(m.OpenIDs) != m.Spec.Open || len(m.HistoryIDs) != m.Spec.History {
		return m, errors.New("incomplete fixture manifest")
	}
	return m, nil
}

func generateFixtures(out string, spec fixtureSpec) error {
	if spec.Users < 51 || spec.Suppliers < 2 || spec.Open < 1 || spec.History < 1 {
		return errors.New("fixtures require >=51 users, >=2 suppliers, and positive request counts")
	}
	m := fixtures{Spec: spec, Files: []string{"user.sql", "supplier.sql", "order.sql", "credit.sql"}}
	ids := func(kind string, count int) []string {
		values := make([]string, count)
		for i := range values {
			values[i] = fixtureID(spec.Seed, kind, i)
		}
		return values
	}
	m.UserIDs = ids("user", spec.Users)
	m.LocationIDs = ids("location", spec.Suppliers)
	m.OpenIDs = ids("open", spec.Open)
	m.HistoryIDs = ids("history", spec.History)
	for i, template := range []string{userSQL + eventUserSQL, supplierSQL, orderSQL + eventOrderSQL, creditSQL + eventCreditSQL} {
		prefix := fmt.Sprintf(`-- Private, additive fixtures. Each file commits independently.
BEGIN;
SET LOCAL lock_timeout = '5s';
SELECT pg_advisory_xact_lock(3219, 981);
CREATE FUNCTION pg_temp.perf_id(kind text, n bigint) RETURNS uuid LANGUAGE SQL IMMUTABLE AS
$$ WITH digest AS (SELECT md5('foc-perf:%d:' || kind || ':' || n::text) AS h)
 SELECT (substr(h,1,12) || '8' || substr(h,14,3) ||
 translate(substr(h,17,1),'0123456789abcdef','89ab89ab89ab89ab') || substr(h,18))::uuid FROM digest $$;
CREATE TEMP TABLE perf_settings AS SELECT %d::int AS users, %d::int AS suppliers,
 %d::int AS open_count, %d::int AS history_count, '%s'::timestamptz AS at;
`, spec.Seed, spec.Users, spec.Suppliers, spec.Open, spec.History, spec.At.Format(time.RFC3339))
		if err := os.WriteFile(
			filepath.Join(out, m.Files[i]),
			[]byte(prefix+template+"\nCOMMIT;\n"),
			0600,
		); err != nil {
			return err
		}
	}
	if err := saveJSON(filepath.Join(out, "events.json"), fixtureEvents(spec)); err != nil {
		return err
	}
	return saveJSON(filepath.Join(out, "fixtures.json"), m)
}

// A fresh fixed corpus drives all four deployed consumer contracts. These are
// staged fixtures, not production-outbox crash evidence. Regenerate under a new
// seed before a subsequent throughput or replay run: effects must start absent.
func fixtureEvents(spec fixtureSpec) []eventInput {
	corpus := make([]eventInput, 10000)
	for i := range corpus {
		eventID := fixtureID(spec.Seed, "load-event", i)
		orderID := fixtureID(spec.Seed, "load-order", i)
		requester := fixtureID(spec.Seed, "user", (i/4)%spec.Users)
		courier := fixtureID(spec.Seed, "user", (i/4+1)%spec.Users)
		timestamp := timestamppb.New(spec.At)
		var message proto.Message
		switch i % 4 {
		case 0:
			userID := fixtureID(spec.Seed, "load-user", i)
			corpus[i].Topic, corpus[i].Key = "users.create", userID
			message = &users.UserCreated{
				EventId:     eventID,
				UserId:      userID,
				Email:       userID + "@perf.foc.test",
				DisplayName: "Event performance user",
				CreatedAt:   timestamp,
			}
		case 1:
			corpus[i].Topic, corpus[i].Key = "order.status_changed", orderID
			message = &orders.OrderStatusChanged{
				EventId:        eventID,
				OrderId:        orderID,
				Revision:       2,
				PreviousStatus: orders.OrderStatus_ORDER_STATUS_PENDING_RESERVATION,
				Status:         orders.OrderStatus_ORDER_STATUS_AWAITING_COURIER,
				Actor: &orders.Actor{
					Type: orders.ActorType_ACTOR_TYPE_SYSTEM,
				},
				RequesterId:        requester,
				PickupLocationName: "Performance supplier 0",
				OccurredAt:         timestamp,
			}
		case 2:
			corpus[i].Topic, corpus[i].Key = "order.outcome", orderID
			message = &orders.OrderOutcome{
				EventId:         eventID,
				OrderId:         orderID,
				Outcome:         orders.OrderOutcomeType_ORDER_OUTCOME_TYPE_CANCELLED_BEFORE_PICKUP,
				RequesterId:     requester,
				ReservedCredits: 1,
				OccurredAt:      timestamp,
			}
		case 3:
			corpus[i].Topic, corpus[i].Key = "order.courier_warning_requested", orderID
			message = &orders.CourierWarningRequested{
				EventId:    eventID,
				OrderId:    orderID,
				CourierId:  courier,
				Reason:     orders.CourierWarningReason_COURIER_WARNING_REASON_CANCELLED_AFTER_PICKUP,
				OccurredAt: timestamp,
			}
		}
		corpus[i].Payload, _ = protojson.Marshal(message)
	}
	return corpus
}

const userSQL = `
INSERT INTO users(id,email,display_name,description,phone_number,role,created_at,updated_at)
SELECT pg_temp.perf_id('user',i), pg_temp.perf_id('user',i)::text || '@perf.foc.test',
 'Performance ' || i, 'Private performance fixture', '+6591234567', 'user', at, at
FROM perf_settings, generate_series(0,users-1) i ON CONFLICT (id) DO NOTHING;
INSERT INTO allowed_email_domains(id,domain)
VALUES(pg_temp.perf_id('domain',0),'perf.foc.test') ON CONFLICT DO NOTHING;
-- Marked published because credit.sql provisions the matching accounts/ledger.
INSERT INTO user_created_outbox(event_id,user_id,email,display_name,user_created_at,payload_version,published_at)
SELECT pg_temp.perf_id('user-event',i), pg_temp.perf_id('user',i),
 pg_temp.perf_id('user',i)::text || '@perf.foc.test', 'Performance ' || i, at, 1, at
FROM perf_settings, generate_series(0,users-1) i ON CONFLICT DO NOTHING;
`

const supplierSQL = `
INSERT INTO buildings(id,name,center,radius_m)
SELECT pg_temp.perf_id('building',i), 'Performance building ' || pg_temp.perf_id('building',i),
 ST_SetSRID(ST_MakePoint(103.77+i*0.0001,1.296),4326)::geography, 200
FROM generate_series(0,9) i ON CONFLICT DO NOTHING;
INSERT INTO categories(id,name) VALUES(pg_temp.perf_id('category',0),'Performance fixtures '||pg_temp.perf_id('category',0)) ON CONFLICT DO NOTHING;
INSERT INTO locations(id,name,is_supplier,building_id,coordinates,details)
SELECT pg_temp.perf_id('location',i), 'Performance supplier ' || i, true, pg_temp.perf_id('building',i%10),
 ST_SetSRID(ST_MakePoint(103.77+(i%100)*0.00001,1.296+(i/100)*0.00001),4326)::geography,
 'Seeded performance supplier ' || i
FROM perf_settings, generate_series(0,suppliers-1) i ON CONFLICT DO NOTHING;
INSERT INTO location_categories(location_id,category_id)
SELECT pg_temp.perf_id('location',i),pg_temp.perf_id('category',0)
FROM perf_settings,generate_series(0,suppliers-1) i ON CONFLICT DO NOTHING;
`

const orderSQL = `
INSERT INTO orders(id,requester_id,status,pickup,delivery,item_description,reward_credits,reserved_credits,
 delivery_duration_ns,expires_at,closed_at,revision,created_at,updated_at)
SELECT pg_temp.perf_id(kind,i),pg_temp.perf_id('user',i%users),
 CASE WHEN kind='open' THEN 'awaiting_courier' ELSE 'cancelled' END,
 jsonb_build_object('location_id',pg_temp.perf_id('location',i%suppliers),'name','Performance supplier '||(i%suppliers),
 'building_name','Performance building '||pg_temp.perf_id('building',(i%suppliers)%10)),
 jsonb_build_object('location_id',pg_temp.perf_id('location',(i+1)%suppliers),'name','Performance supplier '||((i+1)%suppliers),
 'building_name','Performance building '||pg_temp.perf_id('building',((i+1)%suppliers)%10)),
 'Private performance fixture',1,1,1800000000000,at+interval '7 days',
 CASE WHEN kind='history' THEN at-interval '1 day' END,CASE WHEN kind='open' THEN 2 ELSE 3 END,
 at-interval '2 days'-i*interval '1 second', at-interval '1 day'
FROM perf_settings, (VALUES('open'),('history')) kinds(kind),
 LATERAL generate_series(0,CASE WHEN kind='open' THEN open_count ELSE history_count END-1) i
ON CONFLICT DO NOTHING;
INSERT INTO order_audit(order_id,revision,action,actor_kind,previous_status,resulting_status,occurred_at,details)
SELECT id,1,'created','system','','pending_reservation',created_at,'{}' FROM orders
WHERE id IN (SELECT pg_temp.perf_id(kind,i) FROM perf_settings,(VALUES('open'),('history')) kinds(kind),
 LATERAL generate_series(0,CASE WHEN kind='open' THEN open_count ELSE history_count END-1) i)
ON CONFLICT DO NOTHING;
INSERT INTO order_audit(order_id,revision,action,actor_kind,previous_status,resulting_status,occurred_at,details)
SELECT order_id,2,'reservation_confirmed','system','pending_reservation','awaiting_courier',o.created_at+interval '1 second','{}'
FROM order_audit a JOIN orders o ON o.id=a.order_id WHERE a.revision=1 AND a.action='created'
AND o.id IN (SELECT pg_temp.perf_id(kind,i) FROM perf_settings,(VALUES('open'),('history')) kinds(kind),
 LATERAL generate_series(0,CASE WHEN kind='open' THEN open_count ELSE history_count END-1) i)
ON CONFLICT DO NOTHING;
INSERT INTO order_audit(order_id,revision,action,actor_kind,previous_status,resulting_status,occurred_at,details)
SELECT pg_temp.perf_id('history',i),3,'cancelled','system','awaiting_courier','cancelled',at-interval '1 day','{}'
FROM perf_settings,generate_series(0,history_count-1) i ON CONFLICT DO NOTHING;
-- Historical outcomes already reconciled by credit.sql; never publish synthetic fixture history.
`

const creditSQL = `
INSERT INTO accounts(user_id,balance,reserved,created_at,updated_at)
SELECT pg_temp.perf_id('user',i),1000000,
 open_count/users+CASE WHEN i<open_count%users THEN 1 ELSE 0 END,at,at
FROM perf_settings,generate_series(0,users-1) i ON CONFLICT DO NOTHING;
INSERT INTO ledger_entries(id,user_id,kind,balance_delta,reserved_delta)
SELECT pg_temp.perf_id('grant',i),pg_temp.perf_id('user',i),'GRANT',1000000,0
FROM perf_settings,generate_series(0,users-1) i ON CONFLICT DO NOTHING;
INSERT INTO reservations(order_id,requester_id,status,original_amount,reserved_amount,release_reason,
 settlement_outcome,paid_to_courier,returned_to_requester,settled_at)
SELECT pg_temp.perf_id(kind,i),pg_temp.perf_id('user',i%users),
 CASE WHEN kind='open' THEN 'HELD' ELSE 'RELEASED' END,1,CASE WHEN kind='open' THEN 1 ELSE 0 END,
 CASE WHEN kind='history' THEN 'CANCELLED_BEFORE_PICKUP' END,
 CASE WHEN kind='history' THEN 'RELEASED' END,CASE WHEN kind='history' THEN 0 END,
 CASE WHEN kind='history' THEN 1 END,CASE WHEN kind='history' THEN at-interval '1 day' END
FROM perf_settings,(VALUES('open'),('history')) kinds(kind),
 LATERAL generate_series(0,CASE WHEN kind='open' THEN open_count ELSE history_count END-1) i
ON CONFLICT DO NOTHING;
INSERT INTO ledger_entries(id,user_id,kind,balance_delta,reserved_delta,order_id)
SELECT pg_temp.perf_id(kind||'-reserve',i),pg_temp.perf_id('user',i%users),'RESERVE',0,1,pg_temp.perf_id(kind,i)
FROM perf_settings,(VALUES('open'),('history')) kinds(kind),
 LATERAL generate_series(0,CASE WHEN kind='open' THEN open_count ELSE history_count END-1) i
ON CONFLICT DO NOTHING;
INSERT INTO ledger_entries(id,user_id,kind,balance_delta,reserved_delta,order_id)
SELECT pg_temp.perf_id('history-release',i),pg_temp.perf_id('user',i%users),'RELEASE',0,-1,pg_temp.perf_id('history',i)
FROM perf_settings,generate_series(0,history_count-1) i ON CONFLICT DO NOTHING;
`

const eventUserSQL = `
-- Additional fresh users deliberately await their corpus users.create event.
INSERT INTO users(id,email,display_name,phone_number,role,created_at,updated_at)
SELECT pg_temp.perf_id('load-user',i),pg_temp.perf_id('load-user',i)::text||'@perf.foc.test',
 'Event performance user','+6591234567','user',at,at
FROM perf_settings,generate_series(0,9999) i WHERE i%4=0 ON CONFLICT DO NOTHING;
`

const eventOrderSQL = `
INSERT INTO orders(id,requester_id,courier_id,status,pickup,delivery,item_description,reward_credits,reserved_credits,
 delivery_duration_ns,expires_at,assigned_at,picked_up_at,closed_at,revision,created_at,updated_at)
SELECT pg_temp.perf_id('load-order',i),pg_temp.perf_id('user',(i/4)%users),
 CASE WHEN i%4=3 THEN pg_temp.perf_id('user',(i/4+1)%users) END,
 CASE i%4 WHEN 1 THEN 'awaiting_courier' WHEN 2 THEN 'cancelled' ELSE 'failed' END,
 jsonb_build_object('location_id',pg_temp.perf_id('location',0),'name','Performance supplier 0','building_name','Performance building '||pg_temp.perf_id('building',0)),
 jsonb_build_object('location_id',pg_temp.perf_id('location',1),'name','Performance supplier 1','building_name','Performance building '||pg_temp.perf_id('building',1)),
 'Private event performance fixture',1,1,1800000000000,at+interval '7 days',
 CASE WHEN i%4=3 THEN at-interval '10 minutes' END,CASE WHEN i%4=3 THEN at-interval '5 minutes' END,
 CASE WHEN i%4<>1 THEN at END,CASE i%4 WHEN 1 THEN 2 WHEN 2 THEN 3 ELSE 5 END,at-interval '15 minutes',at
FROM perf_settings,generate_series(0,9999) i WHERE i%4<>0 ON CONFLICT DO NOTHING;
INSERT INTO order_audit(order_id,revision,action,actor_kind,previous_status,resulting_status,occurred_at,details)
SELECT pg_temp.perf_id('load-order',i),r,'fixture_lifecycle','system',
 (ARRAY['','pending_reservation','awaiting_courier','awaiting_pickup','delivering'])[r],
 CASE WHEN r=1 THEN 'pending_reservation' WHEN r=2 THEN 'awaiting_courier'
 WHEN i%4=2 THEN 'cancelled' WHEN r=3 THEN 'awaiting_pickup' WHEN r=4 THEN 'delivering' ELSE 'failed' END,
 at,'{}' FROM perf_settings,generate_series(0,9999) i,
 LATERAL generate_series(1,CASE i%4 WHEN 1 THEN 2 WHEN 2 THEN 3 ELSE 5 END) r
WHERE i%4<>0 ON CONFLICT DO NOTHING;
INSERT INTO order_assignment_history(id,order_id,courier_id,assigned_revision,assigned_at,ended_revision,ended_at,end_reason,is_current,source)
SELECT pg_temp.perf_id('load-history',i),pg_temp.perf_id('load-order',i),pg_temp.perf_id('user',(i/4+1)%users),
 3,at-interval '10 minutes',5,at,'courier_cancelled',false,'transition'
FROM perf_settings,generate_series(0,9999) i WHERE i%4=3 ON CONFLICT DO NOTHING;
`

const eventCreditSQL = `
INSERT INTO reservations(order_id,requester_id,status,original_amount,reserved_amount)
SELECT pg_temp.perf_id('load-order',i),pg_temp.perf_id('user',(i/4)%users),'HELD',1,1
FROM perf_settings,generate_series(0,9999) i WHERE i%4<>0 ON CONFLICT DO NOTHING;
INSERT INTO assignments(order_id,assignment_id,courier_id,offer_amount,status)
SELECT pg_temp.perf_id('load-order',i),pg_temp.perf_id('load-assignment',i),pg_temp.perf_id('user',(i/4+1)%users),1,'APPLIED'
FROM perf_settings,generate_series(0,9999) i WHERE i%4=3 ON CONFLICT DO NOTHING;
UPDATE reservations SET status='ASSIGNED',assignment_id=pg_temp.perf_id('load-assignment',i)
FROM generate_series(0,9999) i WHERE i%4=3 AND order_id=pg_temp.perf_id('load-order',i) AND status='HELD';
UPDATE reservations SET status='RELEASED',reserved_amount=0,release_reason='FAILED',settlement_outcome='RELEASED',
 paid_to_courier=0,returned_to_requester=1,settled_at=at
FROM perf_settings,generate_series(0,9999) i WHERE i%4=3 AND order_id=pg_temp.perf_id('load-order',i) AND status='ASSIGNED';
WITH reserved AS (
 INSERT INTO ledger_entries(id,user_id,kind,balance_delta,reserved_delta,order_id)
 SELECT pg_temp.perf_id('load-reserve',i),pg_temp.perf_id('user',(i/4)%users),'RESERVE',0,1,pg_temp.perf_id('load-order',i)
 FROM perf_settings,generate_series(0,9999) i WHERE i%4<>0 ON CONFLICT DO NOTHING RETURNING user_id,reserved_delta
), released AS (
 INSERT INTO ledger_entries(id,user_id,kind,balance_delta,reserved_delta,order_id)
 SELECT pg_temp.perf_id('load-release',i),pg_temp.perf_id('user',(i/4)%users),'RELEASE',0,-1,pg_temp.perf_id('load-order',i)
 FROM perf_settings,generate_series(0,9999) i WHERE i%4=3 ON CONFLICT DO NOTHING RETURNING user_id,reserved_delta
), changes AS (
 SELECT user_id,sum(reserved_delta) delta FROM (SELECT * FROM reserved UNION ALL SELECT * FROM released) entries
 GROUP BY user_id HAVING sum(reserved_delta)<>0
)
UPDATE accounts a SET reserved=a.reserved+c.delta FROM changes c WHERE a.user_id=c.user_id;
`
