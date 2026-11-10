package main

import (
	"context"
	"errors"
	"time"
)

type orderResponse struct {
	Order struct {
		ID       string `json:"id"`
		Revision string `json:"revision"`
		Status   string `json:"status"`
	} `json:"order"`
}

// runWorkflow uses public authenticated RPCs and observes settlement in Credit.
// Requester cancellation after pickup exercises payout without depending on
// optional photo/AI services to complete the workflow.
func runWorkflow(ctx context.Context, m fixtures, base, path, out string) error {
	var sessions []session
	if err := readJSON(path, &sessions); err != nil {
		return err
	}
	if len(sessions) < 2 || sessions[0].UserID == sessions[1].UserID {
		return errors.New("two independent sessions required")
	}
	clients := make([]*rpcClient, 2)
	for i := range clients {
		c, err := newRPC(base, sessions[i])
		if err != nil {
			return err
		}
		if err := c.refresh(ctx); err != nil {
			return err
		}
		clients[i] = c
		sessions[i] = c.session
		if err := saveJSON(path, sessions); err != nil {
			return err
		}
	}
	db, err := connectDB(ctx, "PERF_CREDIT_DB")
	if err != nil {
		return err
	}
	defer db.Close()
	var beforeRequester, beforeCourier int64
	if err := db.QueryRow(ctx, `SELECT (SELECT balance FROM accounts WHERE user_id=$1),(SELECT balance FROM accounts WHERE user_id=$2)`, sessions[0].UserID, sessions[1].UserID).
		Scan(&beforeRequester, &beforeCourier); err != nil {
		return err
	}
	ids := []string{}
	for phase := 0; phase < 2; phase++ {
		var response orderResponse
		key := fixtureID(m.Spec.Seed, "workflow-"+out+time.Now().UTC().Format(time.RFC3339Nano), phase)
		if err := clients[0].call(ctx, orderPath+"CreateOrder", map[string]any{
			"pickupLocationId": m.LocationIDs[0], "delivery": map[string]string{"locationId": m.LocationIDs[1]},
			"itemDescription": "Private resilience check", "rewardCredits": "1", "deliveryDuration": "1800s",
			"expiresAt": time.Now().UTC().Add(time.Hour).Format(time.RFC3339), "idempotencyKey": key,
		}, &response); err != nil {
			return err
		}
		id := response.Order.ID
		if id == "" {
			return errors.New("create did not return an order")
		}
		ids = append(ids, id)
		if err := waitFor(ctx, 30*time.Second, 100*time.Millisecond, func() (bool, error) {
			err := clients[0].call(ctx, orderPath+"GetOrder", map[string]string{"id": id}, &response)
			return response.Order.Status == "ORDER_STATUS_AWAITING_COURIER", err
		}); err != nil {
			return err
		}
		if phase == 1 {
			var accepted map[string]any
			if err := clients[1].call(
				ctx,
				orderPath+"AcceptOrder",
				map[string]string{"id": id, "attemptId": key},
				&accepted,
			); err != nil {
				return err
			}
			if err := waitFor(ctx, 30*time.Second, 100*time.Millisecond, func() (bool, error) {
				err := clients[1].call(ctx, orderPath+"GetOrder", map[string]string{"id": id}, &response)
				return response.Order.Status == "ORDER_STATUS_AWAITING_PICKUP", err
			}); err != nil {
				return err
			}
			if err := clients[1].call(
				ctx,
				orderPath+"ConfirmPickup",
				map[string]string{"orderId": id},
				&response,
			); err != nil {
				return err
			}
		}
		if err := clients[0].call(
			ctx,
			orderPath+"CancelOrder",
			map[string]string{
				"id":               id,
				"expectedRevision": response.Order.Revision,
				"idempotencyKey":   key,
				"reason":           "Private resilience check",
			},
			&response,
		); err != nil {
			return err
		}
		if response.Order.Status != "ORDER_STATUS_CANCELLED" {
			return errors.New("cancellation did not commit")
		}
		expected := "RELEASED"
		if phase == 1 {
			expected = "SETTLED"
		}
		if err := waitFor(ctx, 30*time.Second, 100*time.Millisecond, func() (bool, error) {
			var status string
			err := db.QueryRow(ctx, `SELECT status FROM reservations WHERE order_id=$1`, id).Scan(&status)
			return status == expected, err
		}); err != nil {
			return err
		}
		var balance map[string]any
		if err := clients[0].call(
			ctx,
			"/credit.v1.CreditAccountService/GetMyAccount",
			struct{}{},
			&balance,
		); err != nil {
			return err
		}
	}
	var afterRequester, afterCourier int64
	if err := db.QueryRow(ctx, `SELECT (SELECT balance FROM accounts WHERE user_id=$1),(SELECT balance FROM accounts WHERE user_id=$2)`, sessions[0].UserID, sessions[1].UserID).
		Scan(&afterRequester, &afterCourier); err != nil {
		return err
	}
	var inconsistent, accounts int
	if err := db.QueryRow(ctx, financialConsistencySQL, []string{sessions[0].UserID, sessions[1].UserID}).
		Scan(&inconsistent, &accounts); err != nil {
		return err
	}
	return recordResult(
		out,
		"workflow",
		map[string]any{
			"orderIds":             ids,
			"requesterDelta":       afterRequester - beforeRequester,
			"courierDelta":         afterCourier - beforeCourier,
			"inconsistentAccounts": inconsistent,
		},
		afterRequester == beforeRequester-1 && afterCourier == beforeCourier+1 && inconsistent == 0 && accounts == 2,
	)
}
