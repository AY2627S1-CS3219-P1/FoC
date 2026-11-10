// Backfill legacy orders using authenticated Supplier discovery. Dry-run is default.
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/clients"
	"github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/pickupbackfill"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/supplier/location/v1/locationv1connect"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	apply := flag.Bool("apply", false, "apply conditional coordinate patches (default: dry-run)")
	dryRun := flag.Bool("dry-run", false, "report candidates without writing")
	flag.Parse()
	if *apply && *dryRun {
		return fmt.Errorf("choose either -apply or -dry-run")
	}
	databaseURL := os.Getenv("DATABASE_URL")
	supplierURL := os.Getenv("SUPPLIER_SERVICE_BASE_URL")
	token := os.Getenv("SUPPLIER_BEARER_TOKEN")
	if databaseURL == "" || supplierURL == "" || token == "" {
		return fmt.Errorf("DATABASE_URL, SUPPLIER_SERVICE_BASE_URL and SUPPLIER_BEARER_TOKEN are required")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx = auth.WithUserBearer(ctx, "Bearer "+token)
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	supplier := &clients.Supplier{Client: locationv1connect.NewLocationDiscoveryServiceClient(
		&http.Client{Timeout: 15 * time.Second, CheckRedirect: clients.NoRedirect}, supplierURL)}
	report, err := pickupbackfill.Run(ctx, pool, supplier, *apply, os.Stdout)
	fmt.Printf("apply=%t candidates=%d resolved=%d updated=%d unresolved=%d\n",
		*apply, report.Candidates, report.Resolved, report.Updated, report.Unresolved)
	return err
}
