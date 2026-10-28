// Package seed applies opt-in development fixtures through User's application logic.
package seed

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/models"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/service"
	"github.com/google/uuid"
)

type User struct {
	ID          uuid.UUID       `json:"id"`
	Email       string          `json:"email"`
	DisplayName string          `json:"display_name"`
	Role        models.RoleName `json:"role"`
	Description string          `json:"description"`
	PhoneNumber *string         `json:"phone_number"`
}

type Domain struct {
	ID     uuid.UUID `json:"id"`
	Domain string    `json:"domain"`
}

type Suspension struct {
	ID      uuid.UUID `json:"id"`
	UserID  uuid.UUID `json:"user_id"`
	ActorID uuid.UUID `json:"actor_id"`
	Reason  string    `json:"reason"`
}

type Fixtures struct {
	Version    int        `json:"version"`
	Users      []User     `json:"users"`
	Domain     Domain     `json:"registration_domain"`
	Suspension Suspension `json:"suspension"`
}

func Load(reader io.Reader) (Fixtures, error) {
	var fixtures Fixtures
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&fixtures); err != nil {
		return Fixtures{}, fmt.Errorf("decode user fixtures: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return Fixtures{}, fmt.Errorf("user fixtures must contain one JSON object")
	}
	return fixtures, nil
}

func (f Fixtures) Validate() error {
	if f.Version != 1 || len(f.Users) == 0 {
		return fmt.Errorf("expected version 1 and at least one fixture user")
	}
	ids := make(map[uuid.UUID]models.RoleName)
	emails := make(map[string]bool)
	for _, user := range f.Users {
		email, err := service.NormalizeEmail(user.Email)
		_, duplicate := ids[user.ID]
		if err != nil || email != user.Email || emails[email] || user.ID == uuid.Nil || duplicate {
			return fmt.Errorf("invalid or duplicate fixture user identity: %s", user.ID)
		}
		if !user.Role.Valid() || strings.TrimSpace(user.DisplayName) == "" ||
			utf8.RuneCountInString(user.DisplayName) > 100 || utf8.RuneCountInString(user.Description) > 500 ||
			(user.PhoneNumber != nil && utf8.RuneCountInString(*user.PhoneNumber) > 20) {
			return fmt.Errorf("invalid fixture profile: %s", user.ID)
		}
		ids[user.ID], emails[email] = user.Role, true
	}
	if f.Domain.ID == uuid.Nil || f.Domain.Domain == "" {
		return fmt.Errorf("registration domain identity is required")
	}
	actorRole, actorExists := ids[f.Suspension.ActorID]
	if f.Suspension.ID == uuid.Nil || ids[f.Suspension.UserID] != models.RoleSuspended ||
		!actorExists || !actorRole.IsAdmin() || strings.TrimSpace(f.Suspension.Reason) == "" {
		return fmt.Errorf("suspension must reference a suspended fixture user and an administrator")
	}
	return nil
}
