// Command server runs the Communication Service: apply migrations, serve the
// Telegram linking API and, when a bot token is configured, run the Telegram
// bot until SIGINT/SIGTERM.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/communication-service/internal/bot"
	"github.com/AY2627S1-CS3219-P1/FoC/communication-service/internal/database"
	"github.com/AY2627S1-CS3219-P1/FoC/communication-service/internal/handlers/health"
	"github.com/AY2627S1-CS3219-P1/FoC/communication-service/internal/handlers/telegramlink"
	"github.com/AY2627S1-CS3219-P1/FoC/communication-service/internal/linking"
	"github.com/AY2627S1-CS3219-P1/FoC/communication-service/internal/router"
	"github.com/AY2627S1-CS3219-P1/FoC/communication-service/internal/telegram"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth/httpauth"
	"github.com/rs/cors"
)

const (
	readHeaderTimeout = 5 * time.Second
	shutdownTimeout   = 10 * time.Second
	// Long-poll wait per getUpdates call; the HTTP timeout must exceed it.
	pollTimeout = telegram.DefaultPollTimeout
	// User Service may still be starting; its public keys are fetched at startup.
	authStartupTimeout = time.Minute
	authRetryInterval  = 2 * time.Second
	// Telegram may be briefly unreachable at startup; only a rejected token is
	// final.
	botRetryDelay = 2 * time.Second
	botMaxDelay   = time.Minute
)

type config struct {
	port          string
	databaseURL   string
	runMigrations bool
	telegramToken string
	telegramAPI   string
}

func loadConfig() (config, error) {
	cfg := config{
		port:          os.Getenv("PORT"),
		databaseURL:   os.Getenv("DATABASE_URL"),
		runMigrations: os.Getenv("RUN_MIGRATIONS") != "false",
		telegramToken: os.Getenv("TELEGRAM_BOT_TOKEN"),
		telegramAPI:   os.Getenv("TELEGRAM_API_URL"),
	}
	if cfg.port == "" {
		cfg.port = "8080"
	}
	if cfg.databaseURL == "" {
		return cfg, errors.New("DATABASE_URL is required")
	}
	return cfg, nil
}

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", "communication-service")
	slog.SetDefault(log)
	fatal := func(msg string, err error) {
		log.Error(msg, "err", err)
		os.Exit(1)
	}
	cfg, err := loadConfig()
	if err != nil {
		fatal("invalid config", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := database.Open(ctx, cfg.databaseURL)
	if err != nil {
		fatal("open database", err)
	}
	defer pool.Close()
	if cfg.runMigrations {
		if err := database.Migrate(ctx, pool); err != nil {
			fatal("migrate", err)
		}
		log.Info("migrations applied")
	}

	authenticator, err := newAuthenticator(ctx, log)
	if err != nil {
		fatal("initialize authentication", err)
	}

	links := &linking.Service{Store: &linking.PostgresStore{Pool: pool}, Now: time.Now}

	// Telegram is optional: without a working token the service still serves,
	// and linking reports unavailable.
	var botUsername atomic.Pointer[string]
	startBot(ctx, cfg, log, links, &botUsername)
	currentBot := func() string {
		if name := botUsername.Load(); name != nil {
			return *name
		}
		return ""
	}

	handler := router.New(router.Services{
		Health:       &health.Handler{TelegramEnabled: func() bool { return currentBot() != "" }},
		TelegramLink: &telegramlink.Handler{Logic: links, BotUsername: currentBot},
	}, authenticator.Authenticate)
	srv := &http.Server{
		Addr:              ":" + cfg.port,
		Handler:           corsConfig().Handler(handler),
		ReadHeaderTimeout: readHeaderTimeout,
	}
	go func() {
		log.Info("listening", "addr", srv.Addr, "telegram_enabled", currentBot() != "")
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

// startBot checks the token and starts polling in the background. A rejected
// token disables Telegram; other failures are retried, so a network problem at
// startup doesn't disable Telegram until a restart. botUsername holds the bot's
// username while it is polling.
func startBot(ctx context.Context, cfg config, log *slog.Logger, links *linking.Service, botUsername *atomic.Pointer[string]) {
	if cfg.telegramToken == "" {
		log.Warn("TELEGRAM_BOT_TOKEN is not set; Telegram is disabled")
		return
	}
	client := telegram.NewClient(cfg.telegramToken, cfg.telegramAPI, &http.Client{Timeout: pollTimeout + 10*time.Second})
	go func() {
		me, err := waitForBot(ctx, client, log, botRetryDelay)
		if err != nil {
			if ctx.Err() == nil {
				log.Error("Telegram is disabled: bot check failed", "err", err)
			}
			return
		}
		log.Info("telegram bot ready", "username", me.Username)

		poller := &telegram.Poller{
			Source:  client,
			Handle:  (&bot.Handler{Sender: client, Linker: links, Log: log}).Handle,
			Log:     log,
			Timeout: pollTimeout,
		}
		botUsername.Store(&me.Username)
		defer botUsername.Store(nil)
		if err := poller.Run(ctx); err != nil {
			log.Error("telegram polling stopped", "err", err)
		}
	}()
}

// botChecker is the part of the Bot API waitForBot needs.
type botChecker interface {
	GetMe(ctx context.Context) (telegram.User, error)
}

// waitForBot calls getMe until it succeeds, Telegram rejects the token, or ctx
// ends. The delay between attempts doubles up to botMaxDelay.
func waitForBot(ctx context.Context, client botChecker, log *slog.Logger, delay time.Duration) (telegram.User, error) {
	for {
		me, err := client.GetMe(ctx)
		if err == nil || errors.Is(err, telegram.ErrUnauthorized) {
			return me, err
		}
		log.Warn("Telegram bot check failed; retrying", "err", err, "delay", delay.String())
		select {
		case <-ctx.Done():
			return telegram.User{}, ctx.Err()
		case <-time.After(delay):
		}
		delay = min(delay*2, botMaxDelay)
	}
}

func newAuthenticator(ctx context.Context, log *slog.Logger) (*httpauth.Authenticator, error) {
	deadline := time.Now().Add(authStartupTimeout)
	for {
		authenticator, err := httpauth.NewUserServiceAuthenticator(ctx)
		if err == nil || time.Now().After(deadline) {
			return authenticator, err
		}
		log.Warn("User Service not ready; retrying", "err", err)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(authRetryInterval):
		}
	}
}

// corsConfig allows credentialed requests from the frontend on any localhost
// port, matching the other services.
func corsConfig() *cors.Cors {
	return cors.New(cors.Options{
		AllowOriginFunc: func(origin string) bool {
			return strings.HasPrefix(origin, "http://localhost:")
		},
		AllowCredentials: true,
		AllowedMethods:   []string{"GET", "POST", "OPTIONS"},
		AllowedHeaders: []string{
			"Authorization",
			"Content-Type",
			"Connect-Protocol-Version",
			"Connect-Timeout-Ms",
		},
	})
}
