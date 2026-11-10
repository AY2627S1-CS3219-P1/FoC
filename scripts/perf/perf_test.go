package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestFixturesAreReproducibleAndDoNotResetOtherData(t *testing.T) {
	spec := fixtureSpec{3219, 51, 2, 10, 20, time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)}
	a, b := t.TempDir(), t.TempDir()
	for _, dir := range []string{a, b} {
		if err := generateFixtures(dir, spec); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range []string{"fixtures.json", "user.sql", "supplier.sql", "order.sql", "credit.sql"} {
		x, err := os.ReadFile(filepath.Join(a, file))
		if err != nil {
			t.Fatal(err)
		}
		y, err := os.ReadFile(filepath.Join(b, file))
		if err != nil {
			t.Fatal(err)
		}
		if string(x) != string(y) {
			t.Fatalf("%s is not reproducible", file)
		}
		if strings.Contains(strings.ToUpper(string(x)), "TRUNCATE") ||
			strings.Contains(strings.ToUpper(string(x)), "DELETE FROM") {
			t.Fatalf("destructive fixture %s", file)
		}
	}
	if fixtureID(3219, "user", 1) == fixtureID(3219, "open", 1) {
		t.Fatal("fixture domains collide")
	}
	for i := range 1000 {
		id := fixtureID(3219, "location", i)
		if id[14] != '8' || !strings.ContainsRune("89ab", rune(id[19])) {
			t.Fatalf("fixture violates browser UUID version/variant validation: %s", id)
		}
	}
}

func TestEventCorpusRejectsUnknownTopicsAndMismatchedKeys(t *testing.T) {
	for _, input := range []eventInput{
		{Topic: "not.deployed", Key: "id", Payload: json.RawMessage(`{}`)},
		{Topic: "users.create", Key: "different", Payload: json.RawMessage(`{"eventId":"event","userId":"user"}`)},
		{Topic: "order.status_changed", Key: "order", Payload: json.RawMessage(`{"unknown":true}`)},
	} {
		if _, err := newEventProbe(input, nil); err == nil {
			t.Fatalf("accepted malformed event: %+v", input)
		}
	}
}

func TestPublicationCadenceRequiresAcceptedEventsInEveryMinute(t *testing.T) {
	start := time.Now()
	receipts := make([]eventReceipt, 10000)
	for i := range receipts {
		receipts[i] = eventReceipt{Accepted: true, Start: start.Add(time.Duration(i) * 60 * time.Millisecond)}
	}
	if !publicationCadence(receipts, start, 10*time.Minute) {
		t.Fatal("valid cadence rejected")
	}
	receipts[5000].Accepted = false
	if publicationCadence(receipts, start, 10*time.Minute) {
		t.Fatal("missing accepted event counted as throughput")
	}
}

// TEST_PERF_DATABASE_URL must point at a disposable PostgreSQL+PostGIS database.
// Every service gets a fresh, uniquely named schema; unrelated schemas survive.
func TestFixtureDatabaseIntegration(t *testing.T) {
	url := os.Getenv("TEST_PERF_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_PERF_DATABASE_URL is not set")
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = admin.Close(ctx) }()
	modules := []struct{ name, dir, env string }{
		{"user", "../../user-service/migrations", "PERF_USER_DB"},
		{"supplier", "../../supplier-service/database/schema", "PERF_SUPPLIER_DB"},
		{"order", "../../order-service/database/schema", "PERF_ORDER_DB"},
		{"credit", "../../credit-service/database/schema", "PERF_CREDIT_DB"},
		{"communication", "../../communication-service/migrations", "PERF_COMMUNICATION_DB"},
	}
	for _, module := range modules {
		schema := "perf_" + module.name + "_" + strings.ReplaceAll(
			fixtureID(3219, t.Name()+time.Now().String(), 0),
			"-",
			"",
		)
		identifier := pgx.Identifier{schema}.Sanitize()
		if _, err := admin.Exec(ctx, "CREATE SCHEMA "+identifier); err != nil {
			t.Fatal(err)
		}
		defer func() { _, _ = admin.Exec(ctx, "DROP SCHEMA "+identifier+" CASCADE") }()
		cfg, err := pgx.ParseConfig(url)
		if err != nil {
			t.Fatal(err)
		}
		cfg.RuntimeParams["search_path"] = schema + ",public"
		conn, err := pgx.ConnectConfig(ctx, cfg)
		if err != nil {
			t.Fatal(err)
		}
		files, err := filepath.Glob(filepath.Join(module.dir, "*.sql"))
		if err != nil {
			t.Fatal(err)
		}
		for _, file := range files {
			data, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			up := strings.Split(string(data), "-- +goose Down")[0]
			if _, err := conn.Exec(ctx, up); err != nil {
				t.Fatalf("%s: %v", file, err)
			}
		}
		_ = conn.Close(ctx)
		// Connection strings are intentionally kept out of failure output.
		separator := "?"
		if strings.Contains(url, "?") {
			separator = "&"
		}
		t.Setenv(module.env, url+separator+"search_path="+schema+",public")
	}
	dir := t.TempDir()
	spec := fixtureSpec{3219, 51, 10, 100, 200, time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)}
	full := os.Getenv("TEST_PERF_FULL_DATASET") == "1"
	if full {
		spec.Users = 20000
		spec.Suppliers = 1000
		spec.Open = 10000
		spec.History = 100000
	}
	if err := generateFixtures(dir, spec); err != nil {
		t.Fatal(err)
	}
	m, err := readFixtures(filepath.Join(dir, "fixtures.json"))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := seedDatabases(ctx, m, dir); err != nil {
			t.Fatal(err)
		}
	}
	// Deliberately small fixture counts fail capacity acceptance while all
	// consistency checks must still succeed after seeding twice.
	if err := verifyDatabases(ctx, m, dir); (full && err != nil) || (!full && err == nil) {
		t.Fatalf("unexpected capacity acceptance result for full=%v: %v", full, err)
	}
	var evidence struct {
		Details struct{ Accounts, InconsistentAccounts, MissingReservations int } `json:"details"`
	}
	data, err := os.ReadFile(filepath.Join(dir, "dataset.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &evidence); err != nil {
		t.Fatal(err)
	}
	if evidence.Details.Accounts != spec.Users || evidence.Details.InconsistentAccounts != 0 ||
		evidence.Details.MissingReservations != 0 {
		t.Fatalf("inconsistent repeated fixtures: %+v", evidence.Details)
	}
	dbs := map[string]*pgxpool.Pool{}
	for _, module := range modules {
		db, err := connectDB(ctx, module.env)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		dbs[module.env] = db
	}
	for _, input := range fixtureEvents(spec)[:4] {
		probe, err := newEventProbe(input, dbs)
		if err != nil {
			t.Fatal(err)
		}
		if err := validateEventSource(ctx, probe, dbs); err != nil {
			t.Fatalf("staged %s: %v", input.Topic, err)
		}
		var already bool
		if err := probe.db.QueryRow(ctx, probe.query, probe.args...).Scan(&already); err != nil {
			t.Fatal(err)
		}
		if already {
			t.Fatalf("%s consumer effect is not fresh", input.Topic)
		}
		if _, err := consumerSnapshot(ctx, probe); err != nil {
			t.Fatalf("%s snapshot: %v", input.Topic, err)
		}
	}
	if _, err := assignmentSnapshot(ctx, dbs["PERF_ORDER_DB"], dbs["PERF_CREDIT_DB"], m.OpenIDs[0]); err != nil {
		t.Fatal(err)
	}
	assertions := []databaseAssertion{
		{
			Name:        "fixture accounts",
			DatabaseEnv: "PERF_CREDIT_DB",
			SQL:         `SELECT count(*) FROM accounts`,
			Expected:    float64(spec.Users),
		},
	}
	assertPath := filepath.Join(dir, "checks.json")
	if err := saveJSON(assertPath, assertions); err != nil {
		t.Fatal(err)
	}
	if err := assertDatabases(ctx, assertPath, dir); err != nil {
		t.Fatal(err)
	}
	assertions[0].SQL = `DELETE FROM accounts RETURNING balance`
	if err := saveJSON(assertPath, assertions); err != nil {
		t.Fatal(err)
	}
	if err := assertDatabases(ctx, assertPath, dir); err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("boundary probe did not enforce read-only: %v", err)
	}
}
