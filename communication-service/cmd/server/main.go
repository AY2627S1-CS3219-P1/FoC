// Command server runs the Communication Service: apply migrations, serve the
// Telegram linking API and, when a bot token is configured, run the Telegram
// bot, send order notifications from Kafka and relay order chat until
// SIGINT/SIGTERM.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/communication-service/internal/bot"
	"github.com/AY2627S1-CS3219-P1/FoC/communication-service/internal/chat"
	"github.com/AY2627S1-CS3219-P1/FoC/communication-service/internal/database"
	db "github.com/AY2627S1-CS3219-P1/FoC/communication-service/internal/database/communicationdb"
	"github.com/AY2627S1-CS3219-P1/FoC/communication-service/internal/handlers/health"
	"github.com/AY2627S1-CS3219-P1/FoC/communication-service/internal/handlers/telegramlink"
	"github.com/AY2627S1-CS3219-P1/FoC/communication-service/internal/linking"
	"github.com/AY2627S1-CS3219-P1/FoC/communication-service/internal/notify"
	"github.com/AY2627S1-CS3219-P1/FoC/communication-service/internal/router"
	"github.com/AY2627S1-CS3219-P1/FoC/communication-service/internal/telegram"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth/httpauth"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/kafka"
	eventsv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/kafkagen/order/events/v1"
	transport "github.com/AY2627S1-CS3219-P1/FoC/pkg/server"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/cors"
)

const (
	// Long-poll wait per getUpdates call; the HTTP timeout must exceed it.
	pollTimeout = telegram.DefaultPollTimeout
	// User Service may still be starting; its public keys are fetched at startup.
	authStartupTimeout = time.Minute
	authRetryInterval  = 2 * time.Second
	// Telegram may be briefly unreachable at startup; only a rejected token is
	// final.
	botRetryDelay = 2 * time.Second
	botMaxDelay   = time.Minute
	// Notification sends are short; keep them from hanging the consumer.
	sendTimeout = 10 * time.Second
	// A stopped notification consumer restarts after a delay that doubles up to
	// the maximum, and replays the event that stopped it.
	consumerGroup        = "communication-service"
	consumerRestartDelay = time.Second
	consumerMaxDelay     = time.Minute
)

type config struct {
	transport     transport.Config
	databaseURL   string
	runMigrations bool
	telegramToken string
	telegramAPI   string
	kafkaBrokers  []string
}

func loadConfig() (config, error) {
	serverConfig, err := transport.LoadConfig(os.Getenv)
	if err != nil {
		return config{}, err
	}
	cfg := config{
		transport:     serverConfig,
		databaseURL:   os.Getenv("DATABASE_URL"),
		runMigrations: os.Getenv("RUN_MIGRATIONS") != "false",
		telegramToken: os.Getenv("TELEGRAM_BOT_TOKEN"),
		telegramAPI:   os.Getenv("TELEGRAM_API_URL"),
	}
	for _, broker := range strings.Split(os.Getenv("KAFKA_BROKERS"), ",") {
		if broker = strings.TrimSpace(broker); broker != "" {
			cfg.kafkaBrokers = append(cfg.kafkaBrokers, broker)
		}
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
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	cfg, err := loadConfig()
	if err != nil {
		fatal("invalid config", err)
	}

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
	chats := &chat.PostgresStore{Pool: pool}
	var workers sync.WaitGroup
	workers.Go(func() {
		chat.RunCleanup(ctx, chats, log, time.Now)
	})
	startBot(ctx, cfg, log, links, chats, &botUsername, func() {
		startNotifications(ctx, cfg, log, links, chats, pool, &workers)
	}, &workers)
	currentBot := func() string {
		if name := botUsername.Load(); name != nil {
			return *name
		}
		return ""
	}

	handler := router.New(router.Services{
		Health:       &health.Handler{DB: pool, TelegramEnabled: func() bool { return currentBot() != "" }},
		TelegramLink: &telegramlink.Handler{Logic: links, BotUsername: currentBot},
	}, authenticator.Authenticate)
	allRoutes := corsConfig().Handler(handler)
	servers, err := cfg.transport.Servers(transport.Handlers{
		Public:   allRoutes,
		Internal: router.Internal(),
	})
	if err != nil {
		fatal("configure listeners", err)
	}
	if err := transport.Run(ctx, transport.ShutdownTimeout, servers...); err != nil {
		fatal("serve", err)
	}
	// The bot may start the consumer, so join both before closing the pool.
	workers.Wait()
}

// startBot checks the token and starts polling in the background. A rejected
// token disables Telegram; other failures are retried, so a network problem at
// startup doesn't disable Telegram until a restart. ready runs once the bot is
// confirmed. botUsername holds the bot's username while it is polling.
func startBot(ctx context.Context, cfg config, log *slog.Logger, links *linking.Service, chats *chat.PostgresStore,
	botUsername *atomic.Pointer[string], ready func(), workers *sync.WaitGroup,
) {
	if cfg.telegramToken == "" {
		log.Warn("TELEGRAM_BOT_TOKEN is not set; Telegram is disabled")
		return
	}
	client := telegram.NewClient(
		cfg.telegramToken,
		cfg.telegramAPI,
		&http.Client{Timeout: pollTimeout + 10*time.Second},
	)
	workers.Go(func() {
		me, err := waitForBot(ctx, client, log, botRetryDelay)
		if err != nil {
			if ctx.Err() == nil {
				log.Error("Telegram is disabled: bot check failed", "err", err)
			}
			return
		}
		log.Info("telegram bot ready", "username", me.Username)

		sender := telegram.NewClient(cfg.telegramToken, cfg.telegramAPI, &http.Client{Timeout: sendTimeout})
		relay := &chat.Relay{Sender: sender, Links: links, Store: chats, Log: log, Now: time.Now}
		poller := &telegram.Poller{
			Source:  client,
			Handle:  (&bot.Handler{Sender: sender, Linker: links, Chat: relay, Log: log}).Handle,
			Log:     log,
			Timeout: pollTimeout,
		}
		botUsername.Store(&me.Username)
		defer botUsername.Store(nil)
		ready()
		if err := poller.Run(ctx); err != nil {
			log.Error("telegram polling stopped", "err", err)
		}
	})
}

// startNotifications consumes order status changes in the background when
// Kafka is configured: it updates the chat participants, then sends
// notifications. It runs only with a working bot, so offsets aren't committed
// for messages that were never sent.
func startNotifications(
	ctx context.Context,
	cfg config,
	log *slog.Logger,
	links *linking.Service,
	chats *chat.PostgresStore,
	pool *pgxpool.Pool,
	workers *sync.WaitGroup,
) {
	if len(cfg.kafkaBrokers) == 0 {
		log.Warn("KAFKA_BROKERS is not set; order notifications are disabled")
		return
	}
	handler := &notify.Handler{
		Sender:       telegram.NewClient(cfg.telegramToken, cfg.telegramAPI, &http.Client{Timeout: sendTimeout}),
		Links:        links,
		Deliveries:   &notify.PostgresDeliveries{Queries: db.New(pool)},
		ReplyTargets: chats,
		Log:          log,
		Now:          time.Now,
	}
	projection := &chat.Projection{Store: chats, Log: log, Now: time.Now}
	handle := func(ctx context.Context, meta kafka.Metadata, e *eventsv1.OrderStatusChanged) error {
		if err := projection.Apply(ctx, e); err != nil {
			return err
		}
		return handler.Handle(ctx, meta, e)
	}
	workers.Go(func() {
		runNotifications(ctx, cfg.kafkaBrokers, consumerGroup, log, handle)
	})
}

// runNotifications restarts the consumer until ctx is cancelled. pkg/kafka
// stops a subscriber on any handler error, so a Telegram outage pauses
// notifications and the restart replays the uncommitted event.
func runNotifications(ctx context.Context, brokers []string, group string, log *slog.Logger,
	handle func(context.Context, kafka.Metadata, *eventsv1.OrderStatusChanged) error,
) {
	delay := consumerRestartDelay
	for {
		subscriber, err := kafka.NewSubscriber(kafka.SubscriberConfig{Brokers: brokers, GroupID: group})
		if err == nil {
			err = kafka.Register(subscriber, notify.Topic,
				func() *eventsv1.OrderStatusChanged { return new(eventsv1.OrderStatusChanged) }, handle)
		}
		if err != nil {
			log.Error("order notifications are disabled", "err", err)
			return
		}

		log.Info("consuming order notifications", "topic", notify.Topic)
		started := time.Now()
		err = subscriber.Run(ctx)
		if ctx.Err() != nil {
			return
		}
		if time.Since(started) > consumerMaxDelay {
			delay = consumerRestartDelay
		}
		retryDelay := notificationRetryDelay(err, delay)
		log.Error("order notifications stopped; restarting", "err", err, "delay", retryDelay.String())
		select {
		case <-ctx.Done():
			return
		case <-time.After(retryDelay):
		}
		delay = min(delay*2, consumerMaxDelay)
	}
}

// notificationRetryDelay keeps Telegram's minimum wait separate from the
// consumer backoff, which remains capped at consumerMaxDelay.
func notificationRetryDelay(err error, backoff time.Duration) time.Duration {
	var apiErr *telegram.APIError
	if errors.As(err, &apiErr) && apiErr.Code == http.StatusTooManyRequests {
		return max(backoff, apiErr.RetryAfter)
	}
	return backoff
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
