package main

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/credit/v1/creditv1connect"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/order/v1/orderv1connect"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/accountchecks"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/service"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/store"
)

func newDeletionService(persistence *store.Store, cfg config) (*service.DeletionService, error) {
	for name, address := range map[string]string{
		"ORDER_SERVICE_BASE_URL": cfg.orderServiceURL,
		"CREDIT_SERVICE_BASE_URL": cfg.creditServiceURL,
	} {
		if address == "" {
			continue // Missing dependencies fail closed when deletion is requested.
		}
		u, err := url.Parse(address)
		if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" ||
			(u.Scheme != "http" && u.Scheme != "https") {
			return nil, fmt.Errorf("%s must be an absolute HTTP(S) service URL", name)
		}
	}
	s := &service.DeletionService{
		Users: persistence.Users,
		WithTransaction: func(ctx context.Context, operation func(service.DeletionStore) error) error {
			return persistence.WithTransaction(ctx, func(tx *store.Store) error {
				return operation(service.DeletionStore{Users: tx.Users, Accounts: tx.AccountDeletion})
			})
		},
	}
	client := &http.Client{Timeout: 5 * time.Second}
	if cfg.orderServiceURL != "" {
		s.Order = accountchecks.Order{Client: orderv1connect.NewOrderInternalServiceClient(client, cfg.orderServiceURL)}
	}
	if cfg.creditServiceURL != "" {
		s.Credit = accountchecks.Credit{Client: creditv1connect.NewCreditInternalServiceClient(client, cfg.creditServiceURL)}
	}
	return s, nil
}
