package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/database"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/seed"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/store"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/store/seedstore"
	"github.com/joho/godotenv"
)

func main() {
	path := flag.String("fixtures", "../data/seeds/users.json", "development user fixture file")
	reset := flag.Bool("reset", false, "restore fixture profiles and roles, preserving Credit history")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, *path, *reset); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, path string, reset bool) error {
	if err := godotenv.Load(); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if os.Getenv("APP_ENV") != "local" {
		return fmt.Errorf("development fixtures require APP_ENV=local")
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close() //nolint:errcheck
	fixtures, err := seed.Load(file)
	if err != nil {
		return err
	}
	db, err := database.Open(os.Getenv("DATABASE_URL"), 2, 2)
	if err != nil {
		return err
	}
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	defer sqlDB.Close() //nolint:errcheck
	persistence := seedstore.New(db)
	seeder := seed.Seeder{WithTransaction: func(ctx context.Context, operation func(seed.Transaction) error) error {
		return persistence.WithTransaction(ctx, func(tx *store.Store, fixtures *seedstore.Store) error {
			return operation(seed.Transaction{Users: tx.Users, Events: tx.UserCreatedOutbox, Fixtures: fixtures})
		})
	}}
	if err := seeder.Run(ctx, fixtures, reset); err != nil {
		return err
	}
	fmt.Printf("Applied %d user fixtures; newly created users queue Credit provisioning events.\n", len(fixtures.Users))
	return nil
}
