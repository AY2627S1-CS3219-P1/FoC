// Package app wires repositories, services and handlers into one router.
package app

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"gorm.io/gorm"

	"user-service/internal/admin"
	"user-service/internal/auth"
	"user-service/internal/clock"
	"user-service/internal/config"
	"user-service/internal/mail"
	"user-service/internal/user"
)

type Deps struct {
	DB        *gorm.DB
	Config    config.Config
	Log       *slog.Logger
	Mailer    mail.Mailer
	Clock     clock.Clock
	Suppliers user.SupplierChecker
	AsyncMail bool
}

func NewRouter(d Deps) http.Handler {
	if d.Clock == nil {
		d.Clock = clock.Real{}
	}
	if d.Suppliers == nil {
		d.Suppliers = user.AllowAllSuppliers{}
	}
	ac := d.Config.Auth

	authSvc := auth.NewService(auth.NewRepository(d.DB), d.Mailer, d.Clock, ac, d.Log, d.AsyncMail)
	authH := auth.NewHandler(authSvc, d.Log, ac.CookieName, ac.CookieSecure)
	userH := user.NewHandler(user.NewService(user.NewRepository(d.DB), d.Suppliers, d.Clock), d.Log)
	adminH := admin.NewHandler(admin.NewService(admin.NewRepository(d.DB), d.Clock), d.Log)

	r := chi.NewRouter()
	r.Use(middleware.RequestID) // correlation id (NFR-10.1)
	r.Use(middleware.RealIP)    // trust X-Forwarded-For from the gateway
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(10 * time.Second))

	sqlDB, _ := d.DB.DB()
	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if err := sqlDB.PingContext(r.Context()); err != nil {
			http.Error(w, "db unavailable", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("ok"))
	})

	r.Group(func(r chi.Router) {
		r.Use(auth.Middleware(authSvc, ac.CookieName, d.Log))

		// Public: register / login. Logout checks the session itself.
		authH.Register(r)

		// Everything else needs a session (NFR-05.2.1).
		r.Group(func(r chi.Router) {
			r.Use(auth.RequireAuth)
			userH.Register(r)
			adminH.Register(r)
		})
	})
	return r
}
