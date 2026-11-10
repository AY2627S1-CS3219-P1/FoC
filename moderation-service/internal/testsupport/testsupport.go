// Package testsupport holds integration-test helpers shared by the domain
// packages: the migrated database, an in-memory User Service and the recorded
// outcomes.
package testsupport

import (
	"context"
	"database/sql"
	"os"
	"sync"
	"testing"

	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
	"google.golang.org/protobuf/proto"

	"github.com/AY2627S1-CS3219-P1/FoC/moderation-service/database"
	"github.com/AY2627S1-CS3219-P1/FoC/moderation-service/internal/users"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth/tokenclaims"
	eventsv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/kafkagen/moderation/events/v1"
)

// Database opens TEST_DATABASE_URL and applies the migrations, or skips the
// test when it is unset. The database must be a throwaway one.
func Database(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := database.Migrate(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	return db
}

// Directory stands in for User Service.
type Directory struct {
	mu       sync.Mutex
	people   map[uuid.UUID]users.Person
	reviews  map[uuid.UUID]users.ReviewFacts
	warnings map[uuid.UUID]users.WarningFacts
}

// NewDirectory returns an empty User Service.
func NewDirectory() *Directory {
	return &Directory{
		people:   map[uuid.UUID]users.Person{},
		reviews:  map[uuid.UUID]users.ReviewFacts{},
		warnings: map[uuid.UUID]users.WarningFacts{},
	}
}

// User registers a user and returns their ID.
func (d *Directory) User(name string, role tokenclaims.Role) uuid.UUID {
	d.mu.Lock()
	defer d.mu.Unlock()
	id := uuid.New()
	d.people[id] = users.Person{ID: id, Name: name, Role: role}
	return id
}

// Review registers a live review and returns its ID.
func (d *Directory) Review(reviewer, reviewee, order uuid.UUID) uuid.UUID {
	d.mu.Lock()
	defer d.mu.Unlock()
	id := uuid.New()
	d.reviews[id] = users.ReviewFacts{ReviewerID: reviewer, RevieweeID: reviewee, OrderID: order}
	return id
}

// RemoveReview marks a review removed, by reportID or by an administrator
// when reportID is uuid.Nil.
func (d *Directory) RemoveReview(id, reportID uuid.UUID) {
	d.mu.Lock()
	defer d.mu.Unlock()
	review := d.reviews[id]
	review.Removed, review.RemovalReportID = true, reportID
	d.reviews[id] = review
}

// Warning registers an active warning and returns its ID.
func (d *Directory) Warning(userID, requestID, reportID uuid.UUID) uuid.UUID {
	d.mu.Lock()
	defer d.mu.Unlock()
	id := uuid.New()
	d.warnings[id] = users.WarningFacts{UserID: userID, RequestID: requestID, ReportID: reportID, Active: true}
	return id
}

func (d *Directory) People(_ context.Context, ids []uuid.UUID) (map[uuid.UUID]users.Person, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := map[uuid.UUID]users.Person{}
	for _, id := range ids {
		if person, ok := d.people[id]; ok {
			out[id] = person
		}
	}
	return out, nil
}

func (d *Directory) ReviewFacts(_ context.Context, id uuid.UUID) (users.ReviewFacts, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	review, ok := d.reviews[id]
	if !ok {
		return users.ReviewFacts{}, users.ErrNotFound
	}
	return review, nil
}

func (d *Directory) WarningFacts(_ context.Context, id uuid.UUID) (users.WarningFacts, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	warning, ok := d.warnings[id]
	if !ok {
		return users.WarningFacts{}, users.ErrNotFound
	}
	return warning, nil
}

// Outcomes decodes every recorded outcome about reportID or appealID, oldest
// first.
func Outcomes(t *testing.T, db *sql.DB, reportID, appealID uuid.UUID) []*eventsv1.OutcomeRequested {
	t.Helper()
	rows, err := db.Query("SELECT payload FROM outcome_outbox ORDER BY occurred_at, event_id")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close() //nolint:errcheck

	var out []*eventsv1.OutcomeRequested
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			t.Fatal(err)
		}
		event := new(eventsv1.OutcomeRequested)
		if err := proto.Unmarshal(payload, event); err != nil {
			t.Fatal(err)
		}
		if (reportID != uuid.Nil && event.GetReportId() == reportID.String()) ||
			(appealID != uuid.Nil && event.GetAppealId() == appealID.String()) {
			out = append(out, event)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}
