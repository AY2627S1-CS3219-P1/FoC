package main

import (
	"context"
	"sync"

	"github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/order"
)

type timerRepository interface {
	order.ExpiryTimerRepository
	order.DeliveryTimerRepository
}

// Both timers share the service worker lifetime, not the availability of Credit,
// Kafka, proof object storage or an authenticated HTTP request.
func startLifecycleTimers(ctx context.Context, workers *sync.WaitGroup, repository timerRepository) {
	expiry := order.NewExpiryTimers(repository, order.ExpiryTimerOptions{})
	delivery := order.NewDeliveryTimers(repository, order.DeliveryTimerOptions{})
	workers.Go(func() { expiry.Run(ctx) })
	workers.Go(func() { delivery.Run(ctx) })
}
