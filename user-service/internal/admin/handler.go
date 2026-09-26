package admin

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

	r.Get("/admin/email-domains", w(h.listDomains))
	r.Post("/admin/email-domains", w(h.addDomain))
	r.Delete("/admin/email-domains/{id}", w(h.deleteDomain))

	r.Put("/users/{id}/role", w(h.changeRole))
	r.Get("/users/{id}/role-changes", w(h.roleHistory))

	r.Get("/users/me/warnings", w(h.myWarnings))
	r.Get("/users/{id}/warnings", w(h.userWarnings))
	r.Post("/users/{id}/warnings", w(h.issueWarning))
	r.Post("/warnings/{id}/remove", w(h.removeWarning))
}

// ---- DTOs ----

type domainResponse struct {
	ID        uuid.UUID  `json:"id"`
	Domain    string     `json:"domain"`
	CreatedBy *uuid.UUID `json:"created_by"`
	CreatedAt time.Time  `json:"created_at"`
}

type roleChangeResponse struct {
	ID        uuid.UUID       `json:"id"`
	UserID    uuid.UUID       `json:"user_id"`
	FromRole  models.RoleName `json:"from_role"`
	ToRole    models.RoleName `json:"to_role"`
	Reason    *string         `json:"reason"`
	ActorID   *uuid.UUID      `json:"actor_id"`
	ReportID  *uuid.UUID      `json:"report_id"`
	CreatedAt time.Time       `json:"created_at"`
}

type warningResponse struct {
	ID            uuid.UUID            `json:"id"`
	UserID        uuid.UUID            `json:"user_id"`
	RequestID     uuid.UUID            `json:"request_id"`
	ReportID      *uuid.UUID           `json:"report_id"`
	Reason        string               `json:"reason"`
	Status        models.WarningStatus `json:"status"`
	CreatedAt     time.Time            `json:"created_at"`
	RemovedAt     *time.Time           `json:"removed_at"`
	RemovedReason *string              `json:"removed_reason"`
	AppealID      *uuid.UUID           `json:"appeal_id"`
}

func toDomain(d *models.AllowedEmailDomain) domainResponse {
	return domainResponse{d.ID, d.Domain, d.CreatedBy, d.CreatedAt.UTC()}
}

func toRoleChange(c *models.RoleChange) roleChangeResponse {
	return roleChangeResponse{c.ID, c.UserID, c.FromRole, c.ToRole, c.Reason, c.ActorID, c.ReportID, c.CreatedAt.UTC()}
}

func toWarning(w *models.AccountWarning) warningResponse {
	return warningResponse{w.ID, w.UserID, w.RequestID, w.ReportID, w.Reason, w.Status,
		w.CreatedAt.UTC(), w.RemovedAt, w.RemovedReason, w.AppealID}
}

func list[T, R any](items []T, fn func(*T) R) map[string][]R {
	out := make([]R, len(items))
	for i := range items {
		out[i] = fn(&items[i])
	}
	return map[string][]R{"data": out}
}

// requireAdmin returns the caller if they are admin or super_admin.
func requireAdmin(r *http.Request) (auth.Principal, error) {
	p, err := auth.Require(r.Context())
	if err != nil {
		return p, err
	}
	if !p.IsAdmin() {
		return p, apperr.ErrForbidden
	}
	return p, nil
}

// ---- email domains: admins ----

func (h *Handler) listDomains(w http.ResponseWriter, r *http.Request) error {
	if _, err := requireAdmin(r); err != nil {
		return err
	}
	ds, err := h.svc.ListDomains(r.Context())
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, list(ds, toDomain))
	return nil
}

func (h *Handler) addDomain(w http.ResponseWriter, r *http.Request) error {
	p, err := requireAdmin(r)
	if err != nil {
		return err
	}
	var req struct {
		Domain string `json:"domain"`
	}
	if err := httpx.Decode(w, r, &req); err != nil {
		return err
	}
	d, err := h.svc.AddDomain(r.Context(), p.UserID, req.Domain)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusCreated, toDomain(d))
	return nil
}

func (h *Handler) deleteDomain(w http.ResponseWriter, r *http.Request) error {
	if _, err := requireAdmin(r); err != nil {
		return err
	}
	id, err := httpx.UUIDParam(r, "id")
	if err != nil {
		return err
	}
	if err := h.svc.DeleteDomain(r.Context(), id); err != nil {
		return err
	}
	httpx.NoContent(w)
	return nil
}

// ---- role changes ----

// changeRole: allowed per Principal.CanAssignRole (admin: user<->suspended;
// super_admin: also <-> admin; never self, never super_admin).
func (h *Handler) changeRole(w http.ResponseWriter, r *http.Request) error {
	p, err := auth.Require(r.Context())
	if err != nil {
		return err
	}
	id, err := httpx.UUIDParam(r, "id")
	if err != nil {
		return err
	}
	var req struct {
		Role     models.RoleName `json:"role"`
		Reason   *string         `json:"reason"`
		ReportID *uuid.UUID      `json:"report_id"`
	}
	if err := httpx.Decode(w, r, &req); err != nil {
		return err
	}
	target, err := h.svc.GetUser(r.Context(), id)
	if err != nil {
		return err
	}
	// A no-op change by someone who manages the target falls through to the
	// service's "already has this role" validation error.
	noop := req.Role == target.Role && !p.IsSelf(target.ID) && p.CanManage(target.Role)
	if !noop && !p.CanAssignRole(target.ID, target.Role, req.Role) {
		return apperr.ErrForbidden.WithMessage("you can't change this user to that role")
	}
	c, err := h.svc.ChangeRole(r.Context(), p.UserID, target, ChangeRoleInput{To: req.Role, Reason: req.Reason, ReportID: req.ReportID})
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"user": dto.Full(target), "change": toRoleChange(c)})
	return nil
}

// roleHistory: the user themself or any admin.
func (h *Handler) roleHistory(w http.ResponseWriter, r *http.Request) error {
	p, err := auth.Require(r.Context())
	if err != nil {
		return err
	}
	id, err := httpx.UUIDParam(r, "id")
	if err != nil {
		return err
	}
	if !p.IsSelf(id) && !p.IsAdmin() {
		return apperr.ErrForbidden
	}
	cs, err := h.svc.RoleHistory(r.Context(), id)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, list(cs, toRoleChange))
	return nil
}

// ---- warnings ----

// myWarnings: U7.4, the affected user views their warnings.
func (h *Handler) myWarnings(w http.ResponseWriter, r *http.Request) error {
	p, err := auth.Require(r.Context())
	if err != nil {
		return err
	}
	ws, err := h.svc.Warnings(r.Context(), p.UserID)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, list(ws, toWarning))
	return nil
}

// userWarnings: the user themself or any admin.
func (h *Handler) userWarnings(w http.ResponseWriter, r *http.Request) error {
	p, err := auth.Require(r.Context())
	if err != nil {
		return err
	}
	id, err := httpx.UUIDParam(r, "id")
	if err != nil {
		return err
	}
	if !p.IsSelf(id) && !p.IsAdmin() {
		return apperr.ErrForbidden
	}
	ws, err := h.svc.Warnings(r.Context(), id)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, list(ws, toWarning))
	return nil
}

// issueWarning: an admin who manages the target's role; never self.
// 201 when created, 200 when source_event_id was already processed.
func (h *Handler) issueWarning(w http.ResponseWriter, r *http.Request) error {
	p, err := auth.Require(r.Context())
	if err != nil {
		return err
	}
	id, err := httpx.UUIDParam(r, "id")
	if err != nil {
		return err
	}
	var req struct {
		RequestID     uuid.UUID  `json:"request_id"`
		ReportID      *uuid.UUID `json:"report_id"`
		Reason        *string    `json:"reason"`
		SourceEventID *uuid.UUID `json:"source_event_id"`
	}
	if err := httpx.Decode(w, r, &req); err != nil {
		return err
	}
	target, err := h.svc.GetUser(r.Context(), id)
	if err != nil {
		return err
	}
	if p.IsSelf(id) || !p.CanManage(target.Role) {
		return apperr.ErrForbidden
	}
	wr, created, err := h.svc.IssueWarning(r.Context(), IssueWarningInput{
		UserID: id, RequestID: req.RequestID, ReportID: req.ReportID,
		Reason: req.Reason, SourceEventID: req.SourceEventID,
	})
	if err != nil {
		return err
	}
	status := http.StatusCreated
	if !created {
		status = http.StatusOK
	}
	httpx.WriteJSON(w, status, toWarning(wr))
	return nil
}

// removeWarning: an admin who manages the warned user's role (U7.6).
func (h *Handler) removeWarning(w http.ResponseWriter, r *http.Request) error {
	p, err := auth.Require(r.Context())
	if err != nil {
		return err
	}
	id, err := httpx.UUIDParam(r, "id")
	if err != nil {
		return err
	}
	var req struct {
		Reason   *string    `json:"reason"`
		AppealID *uuid.UUID `json:"appeal_id"`
	}
	if err := httpx.Decode(w, r, &req); err != nil {
		return err
	}
	existing, err := h.svc.GetWarning(r.Context(), id)
	if err != nil {
		return err
	}
	owner, err := h.svc.GetUser(r.Context(), existing.UserID)
	if err != nil {
		return err
	}
	if p.IsSelf(owner.ID) || !p.CanManage(owner.Role) {
		return apperr.ErrForbidden
	}
	wr, err := h.svc.RemoveWarning(r.Context(), id, req.Reason, req.AppealID)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, toWarning(wr))
	return nil
}
