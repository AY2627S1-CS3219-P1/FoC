package main

import (
	"context"
	"flag"
	"fmt"
	"log"

	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/seed"
	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/utils/env"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	paths := parseFlags()
	databaseURL := env.Get().DatabaseURL

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

func parseFlags() seed.Paths {
	var paths seed.Paths
	flag.StringVar(&paths.Suppliers, "suppliers", "../data/csv/supplier-seed-data.csv", "Supplier CSV path")
	flag.StringVar(&paths.Buildings, "buildings", "../data/csv/building-seed-data.csv", "Building CSV path")
	flag.StringVar(&paths.OrdinaryLocations, "locations", "../data/csv/location-seed-data.csv", "ordinary Location CSV path")
	flag.Parse()
	return paths
}

func printReport(report seed.Report) {
	resources := []struct {
		name   string
		counts seed.Counts
	}{
		{"buildings", report.Buildings},
		{"categories", report.Categories},
		{"supplier_locations", report.SupplierLocations},
		{"ordinary_locations", report.OrdinaryLocations},
		{"location_categories", report.LocationCategories},
	}
	for _, resource := range resources {
		printCounts(resource.name, resource.counts)
	}
}

func printCounts(resource string, counts seed.Counts) {
	fmt.Printf("%s inserted=%d updated=%d\n", resource, counts.Inserted, counts.Updated)
}
