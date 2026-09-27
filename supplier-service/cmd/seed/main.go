package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/seed"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	var paths seed.Paths
	flag.StringVar(&paths.Suppliers, "suppliers", "../data/csv/supplier-seed-data.csv", "Supplier CSV path")
	flag.StringVar(&paths.Buildings, "buildings", "../data/csv/building-seed-data.csv", "Building CSV path")
	flag.StringVar(&paths.OrdinaryLocations, "locations", "../data/csv/location-seed-data.csv", "ordinary Location CSV path")
	flag.Parse()

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		log.Fatal("DATABASE_URL is required")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		log.Fatalf("connect to database: %v", err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		log.Fatalf("ping database: %v", err)
	}

	report, err := seed.Seed(ctx, pool, paths)
	if err != nil {
		log.Fatalf("seed Supplier Service: %v", err)
	}
	printReport(report)
}

func printReport(report seed.Report) {
	printCounts("buildings", report.Buildings)
	printCounts("categories", report.Categories)
	printCounts("supplier_locations", report.SupplierLocations)
	printCounts("ordinary_locations", report.OrdinaryLocations)
	printCounts("location_categories", report.LocationCategories)
}

func printCounts(resource string, counts seed.Counts) {
	fmt.Printf("%s inserted=%d updated=%d\n", resource, counts.Inserted, counts.Updated)
}
