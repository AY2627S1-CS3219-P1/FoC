package user

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"user-service/internal/apperr"
	"user-service/internal/auth"
	"user-service/internal/dto"
	"user-service/internal/httpx"
	"user-service/internal/models"
)

type Handler struct {
	svc *Service
	log *slog.Logger
}

func NewHandler(svc *Service, log *slog.Logger) *Handler {
	return &Handler{svc: svc, log: log}
}

// Register mounts routes. Mount behind auth.RequireAuth.
func (h *Handler) Register(r chi.Router) {
	w := func(fn httpx.HandlerFunc) http.HandlerFunc { return httpx.Wrap(h.log, fn) }
	r.Get("/users/me", w(h.getMe))
	r.Patch("/users/me", w(h.updateMe))
	r.Get("/users/me/favourites", w(h.listFavourites))
	r.Put("/users/me/favourites/{supplierID}", w(h.addFavourite))
	r.Delete("/users/me/favourites/{supplierID}", w(h.removeFavourite))

	r.Get("/users", w(h.list))
	r.Get("/users/{id}", w(h.get))
	r.Patch("/users/{id}", w(h.update))
	r.Delete("/users/{id}", w(h.delete))
}

type updateRequest struct {
	DisplayName    *string `json:"display_name"`
	Description    *string `json:"description"`
	TelegramHandle *string `json:"telegram_handle"`
	PhoneNumber    *string `json:"phone_number"`
}

type favouriteResponse struct {
	SupplierID uuid.UUID `json:"supplier_id"`
	CreatedAt  time.Time `json:"created_at"`
}

// ---- self ----

func (h *Handler) getMe(w http.ResponseWriter, r *http.Request) error {
	p, err := auth.Require(r.Context())
	if err != nil {
		return err
	}
	u, err := h.svc.Get(r.Context(), p.UserID)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, dto.Full(u))
	return nil
}

func (h *Handler) updateMe(w http.ResponseWriter, r *http.Request) error {
	p, err := auth.Require(r.Context())
	if err != nil {
		return err
	}
	return h.doUpdate(w, r, p.UserID)
}

// ---- any user ----

// list: admins only (A1.12). Optional ?role= filter.
func (h *Handler) list(w http.ResponseWriter, r *http.Request) error {
	p, err := auth.Require(r.Context())
	if err != nil {
		return err
	}
	if !p.IsAdmin() {
		return apperr.ErrForbidden
	}
	limit, err := httpx.IntQuery(r, "limit", DefaultPageSize)
	if err != nil {
		return err
	}
	offset, err := httpx.IntQuery(r, "offset", 0)
	if err != nil {
		return err
	}
	params := ListParams{Limit: limit, Offset: offset}.Normalize()
	if role := r.URL.Query().Get("role"); role != "" {
		rn := models.RoleName(role)
		params.Role = &rn
	}
	users, total, err := h.svc.List(r.Context(), params)
	if err != nil {
		return err
	}
	out := dto.Page[dto.FullProfile]{Data: make([]dto.FullProfile, 0, len(users)), Total: total, Limit: params.Limit, Offset: params.Offset}
	for i := range users {
		out.Data = append(out.Data, dto.Full(&users[i]))
	}
	httpx.WriteJSON(w, http.StatusOK, out)
	return nil
}

// get: self and admins see the full profile, everyone else the public one
// (U3.2, NFR-06.1).
func (h *Handler) get(w http.ResponseWriter, r *http.Request) error {
	p, err := auth.Require(r.Context())
	if err != nil {
		return err
	}
	id, err := httpx.UUIDParam(r, "id")
	if err != nil {
		return err
	}
	u, err := h.svc.Get(r.Context(), id)
	if err != nil {
		return err
	}
	if p.IsSelf(id) || p.IsAdmin() {
		httpx.WriteJSON(w, http.StatusOK, dto.Full(u))
	} else {
		httpx.WriteJSON(w, http.StatusOK, dto.Public(u))
	}
	return nil
}

// update: self, or an admin who manages the target's role.
func (h *Handler) update(w http.ResponseWriter, r *http.Request) error {
	p, err := auth.Require(r.Context())
	if err != nil {
		return err
	}
	id, err := httpx.UUIDParam(r, "id")
	if err != nil {
		return err
	}
	if !p.IsSelf(id) {
		target, err := h.svc.Get(r.Context(), id)
		if err != nil {
			return err
		}
		if !p.CanManage(target.Role) {
			return apperr.ErrForbidden
		}
	}
	return h.doUpdate(w, r, id)
}

// delete: an admin who manages the target's role; never yourself.
func (h *Handler) delete(w http.ResponseWriter, r *http.Request) error {
	p, err := auth.Require(r.Context())
	if err != nil {
		return err
	}
	id, err := httpx.UUIDParam(r, "id")
	if err != nil {
		return err
	}
	target, err := h.svc.Get(r.Context(), id)
	if err != nil {
		return err
	}
	if p.IsSelf(id) || !p.CanManage(target.Role) {
		return apperr.ErrForbidden
	}
	if err := h.svc.Delete(r.Context(), id); err != nil {
		return err
	}
	httpx.NoContent(w)
	return nil
}

func (h *Handler) doUpdate(w http.ResponseWriter, r *http.Request, id uuid.UUID) error {
	var req updateRequest
	if err := httpx.Decode(w, r, &req); err != nil {
		return err
	}
	u, err := h.svc.Update(r.Context(), id, UpdateInput(req))
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, dto.Full(u))
	return nil
}

// ---- favourites (own only) ----

func (h *Handler) listFavourites(w http.ResponseWriter, r *http.Request) error {
	p, err := auth.Require(r.Context())
	if err != nil {
		return err
	}
	favs, err := h.svc.Favourites(r.Context(), p.UserID)
	if err != nil {
		return err
	}
	out := make([]favouriteResponse, len(favs))
	for i, f := range favs {
		out[i] = favouriteResponse{SupplierID: f.SupplierID, CreatedAt: f.CreatedAt.UTC()}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"data": out})
	return nil
}

func (h *Handler) addFavourite(w http.ResponseWriter, r *http.Request) error {
	p, err := auth.Require(r.Context())
	if err != nil {
		return err
	}
	sid, err := httpx.UUIDParam(r, "supplierID")
	if err != nil {
		return err
	}
	if err := h.svc.AddFavourite(r.Context(), p.UserID, sid); err != nil {
		return err
	}
	httpx.NoContent(w)
	return nil
}

func (h *Handler) removeFavourite(w http.ResponseWriter, r *http.Request) error {
	p, err := auth.Require(r.Context())
	if err != nil {
		return err
	}
	sid, err := httpx.UUIDParam(r, "supplierID")
	if err != nil {
		return err
	}
	if err := h.svc.RemoveFavourite(r.Context(), p.UserID, sid); err != nil {
		return err
	}
	httpx.NoContent(w)
	return nil
}
