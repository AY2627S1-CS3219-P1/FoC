package location

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api/errs"
	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/database/locationdb"
	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/idempotency"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgresMutationStore keeps idempotency, Location, and Category writes in
// the same transaction.
type PostgresMutationStore struct{ pool *pgxpool.Pool }

func NewPostgresMutationStore(pool *pgxpool.Pool) *PostgresMutationStore {
	return &PostgresMutationStore{pool: pool}
}

type postgresMutationTx struct {
	*idempotency.PostgresStore
	queries *locationdb.Queries
	reader  *PostgresReader
}

func (s *PostgresMutationStore) Within(ctx context.Context, run func(MutationTx) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return mapMutationPostgresError("begin location mutation", err)
	}
	defer tx.Rollback(context.WithoutCancel(ctx)) //nolint:errcheck
	queries := locationdb.New(tx)
	unit := &postgresMutationTx{
		PostgresStore: idempotency.NewPostgresStore(tx),
		queries:       queries,
		reader:        NewPostgresReader(queries),
	}
	if err := run(unit); err != nil {
		return err
	}
	return mapMutationPostgresError("commit location mutation", tx.Commit(ctx))
}

// GetForUpdate locks the Location alone. A second statement loads Categories
// after a competing writer releases that lock, avoiding a stale relationship
// snapshot under READ COMMITTED.
func (t *postgresMutationTx) GetForUpdate(ctx context.Context, id string) (Location, error) {
	parsed, err := uuid.Parse(id)
	if err != nil {
		return Location{}, ErrNotFound
	}
	if _, err := t.queries.LockLocation(ctx, parsed); err != nil {
		return Location{}, mapMutationPostgresError("lock location", err)
	}
	return t.reader.GetLocation(ctx, id)
}

func (t *postgresMutationTx) ValidateReferences(ctx context.Context, input Input) error {
	building, err := mutationUUID(input.BuildingID, "building")
	if err != nil {
		return err
	}
	present, err := t.queries.BuildingExists(ctx, building)
	if err != nil {
		return mapMutationPostgresError("check building", err)
	}
	if !present {
		return ErrFailedPrecondition
	}
	categories, err := mutationCategoryIDs(input.CategoryIDs)
	if err != nil {
		return err
	}
	if len(categories) == 0 {
		return nil
	}
	found, err := t.queries.ExistingCategoryIDs(ctx, categories)
	if err != nil {
		return mapMutationPostgresError("check categories", err)
	}
	if len(found) != len(categories) {
		return ErrFailedPrecondition
	}
	return nil
}

func (t *postgresMutationTx) Create(ctx context.Context, input Input, now time.Time) (Location, error) {
	building, err := mutationUUID(input.BuildingID, "building")
	if err != nil {
		return Location{}, err
	}
	categories, err := mutationCategoryIDs(input.CategoryIDs)
	if err != nil {
		return Location{}, err
	}
	if input.Coordinates == nil {
		return Location{}, errs.NewBadRequestError("coordinates are required")
	}
	id := uuid.New()
	err = t.queries.InsertLocation(ctx, locationdb.InsertLocationParams{
		ID: id, Name: input.Name, IsSupplier: input.IsSupplier,
		BuildingID: building, Floor: input.Floor,
		Longitude: input.Coordinates.Longitude, Latitude: input.Coordinates.Latitude,
		OpenFrom: mutationClock(input.OpensAt), OpenTo: mutationClock(input.ClosesAt),
		Contact: input.Contact, Details: input.Details, CreatedAt: now.UTC(),
	})
	if err != nil {
		return Location{}, mapMutationPostgresError("insert location", err)
	}
	if err := t.insertCategories(ctx, id, categories); err != nil {
		return Location{}, err
	}
	return t.reader.GetLocation(ctx, id.String())
}

func (t *postgresMutationTx) Update(ctx context.Context, id string, input Input, expected int64, _ time.Time) (Location, error) {
	parsed, err := mutationUUID(id, "location")
	if err != nil {
		return Location{}, err
	}
	building, err := mutationUUID(input.BuildingID, "building")
	if err != nil {
		return Location{}, err
	}
	categories, err := mutationCategoryIDs(input.CategoryIDs)
	if err != nil {
		return Location{}, err
	}
	if input.Coordinates == nil {
		return Location{}, errs.NewBadRequestError("coordinates are required")
	}
	count, err := t.queries.UpdateLocation(ctx, locationdb.UpdateLocationParams{
		ID: parsed, ExpectedRevision: expected,
		Name: input.Name, IsSupplier: input.IsSupplier, BuildingID: building, Floor: input.Floor,
		Longitude: input.Coordinates.Longitude, Latitude: input.Coordinates.Latitude,
		OpenFrom: mutationClock(input.OpensAt), OpenTo: mutationClock(input.ClosesAt),
		Contact: input.Contact, Details: input.Details,
	})
	if err != nil {
		return Location{}, mapMutationPostgresError("update location", err)
	}
	if count == 0 {
		return Location{}, ErrAborted
	}
	if err := t.queries.DeleteLocationCategories(ctx, parsed); err != nil {
		return Location{}, mapMutationPostgresError("delete location categories", err)
	}
	if err := t.insertCategories(ctx, parsed, categories); err != nil {
		return Location{}, err
	}
	return t.reader.GetLocation(ctx, id)
}

func (t *postgresMutationTx) SetArchived(ctx context.Context, id string, archivedAt *time.Time, expected int64, _ time.Time) (Location, error) {
	parsed, err := mutationUUID(id, "location")
	if err != nil {
		return Location{}, err
	}
	count, err := t.queries.SetLocationArchived(ctx, locationdb.SetLocationArchivedParams{
		ID: parsed, ArchivedAt: archivedAt, ExpectedRevision: expected,
	})
	if err != nil {
		return Location{}, mapMutationPostgresError("archive location", err)
	}
	if count == 0 {
		return Location{}, ErrAborted
	}
	return t.reader.GetLocation(ctx, id)
}

func (t *postgresMutationTx) insertCategories(ctx context.Context, id uuid.UUID, categories []uuid.UUID) error {
	for _, category := range categories {
		if err := t.queries.InsertLocationCategory(ctx, locationdb.InsertLocationCategoryParams{
			LocationID: id, CategoryID: category,
		}); err != nil {
			return mapMutationPostgresError("insert location category", err)
		}
	}
	return nil
}

func mutationUUID(value, label string) (uuid.UUID, error) {
	parsed, err := uuid.Parse(value)
	if err != nil {
		return uuid.UUID{}, errs.NewBadRequestError("invalid " + label + " ID")
	}
	return parsed, nil
}

func mutationCategoryIDs(values []string) ([]uuid.UUID, error) {
	ids := make([]uuid.UUID, 0, len(values))
	seen := make(map[uuid.UUID]bool, len(values))
	for _, value := range values {
		id, err := mutationUUID(value, "category")
		if err != nil {
			return nil, err
		}
		if !seen[id] {
			ids = append(ids, id)
			seen[id] = true
		}
	}
	return ids, nil
}

func mutationClock(value *Clock) pgtype.Time {
	if value == nil {
		return pgtype.Time{}
	}
	return pgtype.Time{Microseconds: (int64(value.Hour)*60 + int64(value.Minute)) * time.Minute.Microseconds(), Valid: true}
}

func mapMutationPostgresError(operation string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	var postgresErr *pgconn.PgError
	if errors.As(err, &postgresErr) {
		switch postgresErr.Code {
		case "23503":
			return ErrFailedPrecondition
		case "23505", "23P01":
			return ErrAlreadyExists
		case "23514", "22003", "22007", "22008":
			return ErrInvalidArgument
		case "40001", "40P01":
			return ErrAborted
		}
	}
	return fmt.Errorf("%s: %w", operation, err)
}

var _ MutationStore = (*PostgresMutationStore)(nil)
var _ MutationTx = (*postgresMutationTx)(nil)
