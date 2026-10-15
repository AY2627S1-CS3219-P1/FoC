// Command seed loads opt-in local account fixtures through User Service's store.
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/database"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/seed"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/store"
)

func main() {
	reset := flag.Bool("reset", false, "restore fixed demo account fields and roles")
	flag.Parse()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		log.Fatal("DATABASE_URL is required")
	}
	db, err := database.Open(dsn, 2, 1)
	if err != nil {
		log.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = sqlDB.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	service := seed.Service{Store: store.NewDemoSeed(db), Clock: time.Now}
	if err := service.Run(ctx, *reset); err != nil {
		log.Fatalf("seed User Service: %v", err)
	}
	log.Print("Demo accounts ready; see data/demo/README.md for emails and roles")
}
