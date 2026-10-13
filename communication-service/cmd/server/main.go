// Command server runs the Communication Service: serve health checks and, when
// a bot token is configured, run the Telegram bot until SIGINT/SIGTERM.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/communication-service/internal/bot"
	"github.com/AY2627S1-CS3219-P1/FoC/communication-service/internal/handlers/health"
	"github.com/AY2627S1-CS3219-P1/FoC/communication-service/internal/router"
	"github.com/AY2627S1-CS3219-P1/FoC/communication-service/internal/telegram"
)

const (
	readHeaderTimeout = 5 * time.Second
	shutdownTimeout   = 10 * time.Second
	// Long-poll wait per getUpdates call; the HTTP timeout must exceed it.
	pollTimeout = 30 * time.Second
)

type config struct {
	port          string
	telegramToken string
	telegramAPI   string
}

func loadConfig() config {
	cfg := config{
		port:          os.Getenv("PORT"),
		telegramToken: os.Getenv("TELEGRAM_BOT_TOKEN"),
		telegramAPI:   os.Getenv("TELEGRAM_API_URL"),
	}
	if cfg.port == "" {
		cfg.port = "8080"
	}
	return cfg
}

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", "communication-service")
	cfg := loadConfig()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Telegram is optional: without a working token the service still serves,
	// so ordering never depends on the bot.
	var telegramEnabled atomic.Bool
	startBot(ctx, cfg, log, &telegramEnabled)

	srv := &http.Server{
		Addr:              ":" + cfg.port,
		Handler:           router.New(router.Services{Health: &health.Handler{TelegramEnabled: telegramEnabled.Load}}),
		ReadHeaderTimeout: readHeaderTimeout,
	}
	go func() {
		log.Info("listening", "addr", srv.Addr, "telegram_enabled", telegramEnabled.Load())
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("serve", "err", err)
			stop()
		}
	}()

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("shutdown", "err", err)
	}
}

// startBot verifies the token and starts polling in the background. enabled
// is true while the bot is polling.
func startBot(ctx context.Context, cfg config, log *slog.Logger, enabled *atomic.Bool) {
	if cfg.telegramToken == "" {
		log.Warn("TELEGRAM_BOT_TOKEN is not set; Telegram is disabled")
		return
	}
	client := telegram.NewClient(cfg.telegramToken, cfg.telegramAPI, &http.Client{Timeout: pollTimeout + 10*time.Second})
	me, err := client.GetMe(ctx)
	if err != nil {
		log.Error("Telegram is disabled: bot check failed", "err", err)
		return
	}
	log.Info("telegram bot ready", "username", me.Username)

	poller := &telegram.Poller{
		Source:  client,
		Handle:  (&bot.Handler{Sender: client, Log: log}).Handle,
		Log:     log,
		Timeout: pollTimeout,
	}
	enabled.Store(true)
	go func() {
		defer enabled.Store(false)
		if err := poller.Run(ctx); err != nil {
			log.Error("telegram polling stopped", "err", err)
		}
	}()
}
