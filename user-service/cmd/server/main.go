package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"user-service/internal/app"
	"user-service/internal/clock"
	"user-service/internal/config"
	"user-service/internal/database"
	"user-service/internal/mail"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	db, err := database.Open(cfg.DatabaseURL, cfg.DBMaxOpen, cfg.DBMaxIdle)
	if err != nil {
		return err
	}
	sqlDB, _ := db.DB()
	defer sqlDB.Close()

	if cfg.RunMigrations {
		if err := database.Migrate(db); err != nil {
			return err
		}
		log.Info("migrations applied")
	}

	handler := app.NewRouter(app.Deps{
		DB:     db,
		Config: cfg,
		Log:    log,
		Mailer: mail.SMTP{
			Addr: cfg.Mail.SMTPAddr, From: cfg.Mail.From,
			Username: cfg.Mail.Username, Password: cfg.Mail.Password,
		},
		Clock:     clock.Real{},
		AsyncMail: true,
	})

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", cfg.HTTPAddr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	log.Info("shutting down")
	return srv.Shutdown(shutdownCtx)
}
