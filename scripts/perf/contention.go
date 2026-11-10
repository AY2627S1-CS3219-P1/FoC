package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func runContention(ctx context.Context, m fixtures, base, path string, rounds int, out string) error {
	var pool []session
	if err := readJSON(path, &pool); err != nil {
		return err
	}
	if rounds < 1 || rounds > len(m.OpenIDs) {
		return errors.New("not enough open fixtures for contention rounds")
	}
	orderDB, err := connectDB(ctx, "PERF_ORDER_DB")
	if err != nil {
		return err
	}
	defer orderDB.Close()
	creditDB, err := connectDB(ctx, "PERF_CREDIT_DB")
	if err != nil {
		return err
	}
	defer creditDB.Close()
	f, err := os.OpenFile(filepath.Join(out, "contention-rounds.jsonl"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	enc := json.NewEncoder(f)
	passed := rounds >= 100
	clientPool := map[string]*rpcClient{}
	for round := 0; round < rounds; round++ {
		orderID := m.OpenIDs[round]
		var requester, status string
		if err := orderDB.QueryRow(ctx, `SELECT requester_id::text,status FROM orders WHERE id=$1`, orderID).
			Scan(&requester, &status); err != nil {
			return err
		}
		if status != "awaiting_courier" {
			return errors.New("contention needs fresh, unassigned open fixtures")
		}
		clients := make([]*rpcClient, 0, 50)
		seen := map[string]bool{}
		for _, s := range pool {
			if s.UserID == requester || seen[s.UserID] {
				continue
			}
			c := clientPool[s.UserID]
			if c == nil {
				c, err = newRPC(base, s)
				if err != nil {
					return err
				}
				clientPool[s.UserID] = c
			}
			if err := c.refresh(ctx); err != nil {
				return err
			}
			clients = append(clients, c)
			seen[s.UserID] = true
			if len(clients) == 50 {
				break
			}
		}
		if len(clients) != 50 {
			return errors.New("50 distinct authenticated couriers are required")
		}
		for i, s := range pool {
			if c := clientPool[s.UserID]; c != nil {
				pool[i] = c.session
			}
		}
		if err := saveJSON(path, pool); err != nil {
			return err
		}
		attempts := make([]map[string]string, 50)
		for i := range attempts {
			attempts[i] = map[string]string{"id": orderID, "attemptId": fixtureID(m.Spec.Seed, "race-"+orderID, i)}
		}
		var ready, done sync.WaitGroup
		ready.Add(50)
		done.Add(50)
		start := make(chan struct{})
		starts := make([]time.Time, 50)
		errorsByCourier := make([]string, 50)
		validCalls := make([]bool, 50)
		responses := make([]map[string]any, 50)
		for i, c := range clients {
			go func(i int, c *rpcClient) {
				defer done.Done()
				ready.Done()
				<-start
				starts[i] = time.Now().UTC()
				var response map[string]any
				err := c.call(ctx, orderPath+"AcceptOrder", attempts[i], &response)
				validCalls[i] = isAssignmentResponse(err, response)
				responses[i] = response
				if err != nil {
					errorsByCourier[i] = err.Error()
				}
			}(i, c)
		}
		ready.Wait()
		released := time.Now().UTC()
		close(start)
		done.Wait()
		// The submitted attemptId is a retry key. The response contains the
		// server's durable workflow ID; verify the mapping rather than equality.
		mappingsValid := true
		for i, response := range responses {
			if !validCalls[i] || errorsByCourier[i] != "" {
				continue
			}
			var matched bool
			attempt := response["attempt"].(map[string]any)
			if err := orderDB.QueryRow(
				ctx,
				`SELECT EXISTS(SELECT 1 FROM order_assignment_attempts
 WHERE id=$1 AND order_id=$2 AND caller_id=$3 AND idempotency_key=$4)`,
				attempt["attemptId"],
				orderID,
				clients[i].session.UserID,
				attempts[i]["attemptId"],
			).Scan(&matched); err != nil {
				return err
			}
			mappingsValid = mappingsValid && matched
		}
		state := struct {
			Winner                                                string
			History, Transitions, Succeeded, Applied, Adjustments int
		}{}
		err = waitFor(ctx, 30*time.Second, 100*time.Millisecond, func() (bool, error) {
			err := orderDB.QueryRow(ctx, `SELECT coalesce(courier_id::text,''),
 (SELECT count(*) FROM order_assignment_history WHERE order_id=o.id),
 (SELECT count(*) FROM order_audit WHERE order_id=o.id AND previous_status='awaiting_courier' AND resulting_status='awaiting_pickup'),
 (SELECT count(*) FROM order_assignment_attempts WHERE order_id=o.id AND phase='succeeded')
 FROM orders o WHERE id=$1`, orderID).Scan(&state.Winner, &state.History, &state.Transitions, &state.Succeeded)
			if err != nil {
				return false, err
			}
			err = creditDB.QueryRow(ctx, `SELECT
 (SELECT count(*) FROM assignments WHERE order_id=$1 AND status='APPLIED'),
 (SELECT count(*) FROM reservation_operations WHERE order_id=$1 AND operation='ADJUST' AND revision IS NOT NULL)`, orderID).Scan(&state.Applied, &state.Adjustments)
			return state.Winner != "" && state.Succeeded > 0 && state.Applied > 0, err
		})
		before, errSnapshot := assignmentSnapshot(ctx, orderDB, creditDB, orderID)
		if errSnapshot != nil {
			return errSnapshot
		}
		replayValid := true
		for i, c := range clients {
			var response map[string]any
			// Reuse every original attempt ID, including all losing attempts.
			err := c.call(ctx, orderPath+"AcceptOrder", attempts[i], &response)
			replayValid = replayValid && isAssignmentResponse(err, response)
			if errorsByCourier[i] == "" && err == nil {
				original, _ := responses[i]["attempt"].(map[string]any)
				replayed, _ := response["attempt"].(map[string]any)
				replayValid = replayValid && original["attemptId"] == replayed["attemptId"]
			}
		}
		after, errSnapshot := assignmentSnapshot(ctx, orderDB, creditDB, orderID)
		if errSnapshot != nil {
			return errSnapshot
		}
		allValid := true
		for _, valid := range validCalls {
			allValid = allValid && valid
		}
		roundPassed := err == nil && allValid && mappingsValid && replayValid && seen[state.Winner] &&
			state.History == 1 &&
			state.Transitions == 1 &&
			state.Succeeded == 1 &&
			state.Applied == 1 &&
			state.Adjustments == 1 &&
			before == after
		passed = passed && roundPassed
		if err := enc.Encode(map[string]any{
			"round":                round,
			"orderId":              orderID,
			"released":             released,
			"callStarts":           starts,
			"attempts":             attempts,
			"errors":               errorsByCourier,
			"state":                state,
			"replayUnchanged":      before == after,
			"validCalls":           validCalls,
			"responses":            responses,
			"attemptMappingsValid": mappingsValid,
			"replayCallsValid":     replayValid,
			"passed":               roundPassed,
		}); err != nil {
			return err
		}
	}
	return recordResult(out, "contention", map[string]any{
		"rounds":              rounds,
		"couriersPerRound":    50,
		"evidence":            "contention-rounds.jsonl",
		"adjustmentAssertion": "one ADJUST operation; equal-price acceptance may produce no nonzero ledger adjustment",
	}, passed)
}

func isAssignmentResponse(err error, response map[string]any) bool {
	if err != nil {
		var rpc *rpcError
		// A fresh attempt arriving after assignment is deliberately hidden as
		// not_found; the pre-barrier database read established the open request.
		return errors.As(err, &rpc) &&
			(rpc.Code == "failed_precondition" || rpc.Code == "aborted" || rpc.Code == "already_exists" || rpc.Code == "not_found")
	}
	attempt, ok := response["attempt"].(map[string]any)
	id, _ := attempt["attemptId"].(string)
	status := attempt["status"]
	return ok && eventUUID.MatchString(id) &&
		(status == "ASSIGNMENT_ATTEMPT_STATUS_PENDING" || status == "ASSIGNMENT_ATTEMPT_STATUS_SUCCEEDED" ||
			status == "ASSIGNMENT_ATTEMPT_STATUS_COMPENSATING")
}

func assignmentSnapshot(ctx context.Context, orders, credits *pgxpool.Pool, id string) (string, error) {
	var a, b string
	err := orders.QueryRow(ctx, `SELECT jsonb_build_object('order',to_jsonb(o),
 'history',(SELECT jsonb_agg(to_jsonb(h) ORDER BY id) FROM order_assignment_history h WHERE order_id=o.id),
 'audit',(SELECT jsonb_agg(to_jsonb(a) ORDER BY revision) FROM order_audit a WHERE order_id=o.id),
 'attempts',(SELECT jsonb_agg(to_jsonb(a) ORDER BY id) FROM order_assignment_attempts a WHERE order_id=o.id))::text
 FROM orders o WHERE id=$1`, id).Scan(&a)
	if err != nil {
		return "", err
	}
	err = credits.QueryRow(ctx, `SELECT jsonb_build_object('reservation',to_jsonb(r),
 'assignments',(SELECT jsonb_agg(to_jsonb(a) ORDER BY assignment_id) FROM assignments a WHERE order_id=r.order_id),
 'ledger',(SELECT jsonb_agg(to_jsonb(l) ORDER BY id) FROM ledger_entries l WHERE order_id=r.order_id),
 'operations',(SELECT jsonb_agg(to_jsonb(p) ORDER BY operation,assignment_id) FROM reservation_operations p WHERE order_id=r.order_id),
 'accounts',(SELECT jsonb_agg(to_jsonb(a) ORDER BY user_id) FROM accounts a WHERE user_id=r.requester_id OR user_id IN
 (SELECT courier_id FROM assignments WHERE order_id=r.order_id)))::text FROM reservations r WHERE order_id=$1`, id).Scan(&b)
	if err != nil {
		return "", err
	}
	return fmt.Sprint(a, "\n", b), nil
}
