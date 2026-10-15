package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/seed"
	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/utils/env"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	paths, demoDirectory, reset := parseFlags()
	databaseURL := env.Get().DatabaseURL

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		log.Fatalf("connect to database: %v", err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		log.Fatalf("ping database: %v", err)
	}

	var report seed.Report
	if demoDirectory != "" {
		report, err = seed.SeedDemo(ctx, pool, demoDirectory, reset, time.Now())
	} else {
		if reset {
			log.Fatal("--reset-demo requires --demo-dir")
		}
		report, err = seed.Seed(ctx, pool, paths)
	}
	if err != nil {
		log.Fatalf("seed Supplier Service: %v", err)
	}
	printReport(report)
}

func parseFlags() (seed.Paths, string, bool) {
	var demoDirectory string
	var reset bool
	flag.StringVar(&demoDirectory, "demo-dir", "", "opt-in local demo CSV directory")
	flag.BoolVar(&reset, "reset-demo", false, "restore fixed demo records; requires --demo-dir")
	var paths seed.Paths
	flag.StringVar(&paths.Suppliers, "suppliers", "../data/csv/supplier-seed-data.csv", "Supplier CSV path")
	flag.StringVar(&paths.Buildings, "buildings", "../data/csv/building-seed-data.csv", "Building CSV path")
	flag.StringVar(&paths.OrdinaryLocations, "locations", "../data/csv/location-seed-data.csv", "ordinary Location CSV path")
	flag.Parse()
	return paths, demoDirectory, reset
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
