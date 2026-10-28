//go:build integration

package seed

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/models"
	"github.com/google/uuid"
)

func TestSeedTransactionsAndRepair(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	db, _ := testPostgres(t, ctx)
	seeder := testSeeder(db)
	fixtures := testFixtures(t)
	reset := func(t *testing.T) {
		t.Helper()
		mustExec(t, db, "TRUNCATE users CASCADE")
		mustExec(t, db, "TRUNCATE user_created_outbox, allowed_email_domains")
	}
	seed := func(t *testing.T, restore bool) {
		t.Helper()
		if err := seeder.Run(ctx, fixtures, restore); err != nil {
			t.Fatal(err)
		}
	}
	const snapshot = "SELECT jsonb_agg(to_jsonb(e) ORDER BY user_id)::text FROM user_created_outbox e"
	const userSnapshot = "SELECT jsonb_agg(to_jsonb(u) ORDER BY id)::text FROM users u"

	t.Run("fresh and concurrent reruns preserve delivery state", func(t *testing.T) {
		reset(t)
		seed(t, false)
		requireCount(t, db, "users", 6)
		requireCount(t, db, "user_created_outbox", 6)
		requireCount(t, db, "role_changes", 1)
		requireCount(t, db, "allowed_email_domains", 1)
		if got := scalar(t, db, `SELECT count(*)::text FROM user_created_outbox e JOIN users u ON u.id=e.user_id
			WHERE e.email=u.email AND e.display_name=u.display_name AND e.user_created_at=u.created_at AND e.payload_version=1`); got != "6" {
			t.Fatalf("matching registration snapshots=%s, want 6", got)
		}
		mustExec(t, db, "UPDATE users SET description='manual edit', role='user' WHERE id=?", fixtures.Users[1].ID)
		mustExec(t, db, "UPDATE user_created_outbox SET published_at=now() WHERE user_id=?", fixtures.Users[0].ID)
		mustExec(
			t,
			db,
			"UPDATE user_created_outbox SET lease_token=?,lease_until=now()+interval '1 minute' WHERE user_id=?",
			uuid.New(),
			fixtures.Users[1].ID,
		)
		mustExec(
			t,
			db,
			"UPDATE user_created_outbox SET available_at=now()+interval '1 hour' WHERE user_id=?",
			fixtures.Users[2].ID,
		)
		original := scalar(t, db, snapshot)
		originalUsers := scalar(t, db, userSnapshot)
		var workers sync.WaitGroup
		errors := make(chan error, 4)
		for range 4 {
			workers.Go(func() { errors <- seeder.Run(ctx, fixtures, false) })
		}
		workers.Wait()
		close(errors)
		for err := range errors {
			if err != nil {
				t.Fatal(err)
			}
		}
		if got := scalar(
			t,
			db,
			"SELECT description || ':' || role FROM users WHERE id=?",
			fixtures.Users[1].ID,
		); got != "manual edit:user" {
			t.Fatalf("rerun changed user edits: %s", got)
		}
		if scalar(t, db, snapshot) != original {
			t.Fatal("rerun changed event identity, payload, or delivery state")
		}
		if scalar(t, db, userSnapshot) != originalUsers {
			t.Fatal("rerun changed user profiles or timestamps")
		}
		unrelated := models.User{Email: "unrelated@example.com", DisplayName: "Unrelated user"}
		if err := db.Create(&unrelated).Error; err != nil {
			t.Fatal(err)
		}
		originalUnrelated := scalar(t, db, "SELECT to_jsonb(u)::text FROM users u WHERE id=?", unrelated.ID)
		seed(t, true)
		if scalar(t, db, "SELECT to_jsonb(u)::text FROM users u WHERE id=?", unrelated.ID) != originalUnrelated {
			t.Fatal("reset changed an unrelated user")
		}
		if scalar(t, db, snapshot) != original {
			t.Fatal("reset changed event identity, payload, or delivery state")
		}
		if got := scalar(t, db, "SELECT role FROM users WHERE id=?", fixtures.Users[1].ID); got != "admin" {
			t.Fatalf("reset did not restore role: %s", got)
		}
		requireCount(t, db, "role_changes", 1)
	})

	t.Run("repair existing users and preserve deleted users until reset", func(t *testing.T) {
		reset(t)
		seed(t, false)
		mustExec(t, db, "TRUNCATE user_created_outbox")
		mustExec(t, db, "UPDATE users SET display_name='Existing edited name' WHERE id=?", fixtures.Users[0].ID)
		mustExec(t, db, "UPDATE users SET deleted_at=now() WHERE id=?", fixtures.Users[1].ID)
		originalUsers := scalar(t, db, userSnapshot)
		seed(t, false)
		if scalar(t, db, userSnapshot) != originalUsers {
			t.Fatal("repair changed existing profiles or deletions")
		}
		requireCount(t, db, "user_created_outbox", 5)
		if got := scalar(
			t,
			db,
			"SELECT display_name FROM user_created_outbox WHERE user_id=?",
			fixtures.Users[0].ID,
		); got != "Existing edited name" {
			t.Fatalf("repair did not snapshot persisted user: %s", got)
		}
		seed(t, true)
		requireCount(t, db, "user_created_outbox", 6)
		if got := scalar(t, db, "SELECT count(*)::text FROM users WHERE deleted_at IS NOT NULL"); got != "0" {
			t.Fatal("reset did not restore deleted fixture")
		}
	})

	t.Run("conflicting identity rolls back earlier fixtures", func(t *testing.T) {
		reset(t)
		unrelated := models.User{Email: fixtures.Users[5].Email, DisplayName: "Existing account"}
		if err := db.Create(&unrelated).Error; err != nil {
			t.Fatal(err)
		}
		if err := seeder.Run(ctx, fixtures, false); err == nil {
			t.Fatal("seed took over an unrelated user's email")
		}
		requireCount(t, db, "users", 1)
		requireCount(t, db, "user_created_outbox", 0)
		requireCount(t, db, "role_changes", 0)
	})

	t.Run("changed fixture email rejects reset without partial writes", func(t *testing.T) {
		reset(t)
		seed(t, false)
		mustExec(t, db, "UPDATE users SET email='changed@example.com' WHERE id=?", fixtures.Users[5].ID)
		originalUsers := scalar(t, db, userSnapshot)
		originalEvents := scalar(t, db, snapshot)
		if err := seeder.Run(ctx, fixtures, true); err == nil {
			t.Fatal("reset accepted a changed fixture identity")
		}
		if scalar(t, db, userSnapshot) != originalUsers || scalar(t, db, snapshot) != originalEvents {
			t.Fatal("identity conflict did not roll back users and events")
		}
	})

	t.Run("event failure rolls back every fixture", func(t *testing.T) {
		reset(t)
		mustExec(t, db, `CREATE FUNCTION fail_seed_event() RETURNS trigger LANGUAGE plpgsql AS $$
			BEGIN RAISE EXCEPTION 'event write failed'; END; $$;
			CREATE TRIGGER fail_seed_event BEFORE INSERT ON user_created_outbox
			FOR EACH ROW EXECUTE FUNCTION fail_seed_event()`)
		defer mustExec(t, db, "DROP TRIGGER fail_seed_event ON user_created_outbox; DROP FUNCTION fail_seed_event()")
		if err := seeder.Run(ctx, fixtures, false); err == nil {
			t.Fatal("seed succeeded despite failed event write")
		}
		requireCount(t, db, "users", 0)
		requireCount(t, db, "user_created_outbox", 0)
		requireCount(t, db, "allowed_email_domains", 0)
	})
}
