package config

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/database"
	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/database/moderationdb"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNoActiveConfiguration reports that no configuration version has been
// activated. It is a normal, expected state while the service still awaits its
// first activation, so it is never surfaced as an RPC error.
var ErrNoActiveConfiguration = errors.New("no active moderation configuration")

// ErrConfigurationConflict reports that an activation was rejected because the
// currently active version no longer matches the expected one.
var ErrConfigurationConflict = errors.New("config: active configuration changed; activation rejected")

// Store persists the immutable configuration versions and the active pointer.
// Writes run inside database.Within; reads run directly against the pool.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore binds the configuration queries to one connection pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// EnsureConfigState makes the config-state singleton row exist so reads never
// see "no rows" merely because the service has not activated anything yet.
func (s *Store) EnsureConfigState(ctx context.Context) error {
	return database.Within(ctx, s.pool, func(q *moderationdb.Queries) error {
		return q.EnsureConfigStateSingleton(ctx)
	})
}

// InsertConfigVersion persists an immutable configuration version and returns
// its id. It never becomes active; activation is a separate, optimistically
// concurrent step.
func (s *Store) InsertConfigVersion(
	ctx context.Context,
	modelID string,
	parameters []byte,
	outputMode string,
	policyVersion string,
	providerFingerprint string,
	createdBy string,
) (uuid.UUID, error) {
	return moderationdb.New(s.pool).InsertConfigVersion(ctx, moderationdb.InsertConfigVersionParams{
		ModelID:             modelID,
		Parameters:          parameters,
		OutputMode:          outputMode,
		PolicyVersion:       policyVersion,
		ProviderFingerprint: providerFingerprint,
		CreatedBy:           createdBy,
	})
}

// GetConfigVersionById returns one configuration version by id, or
// pgx.ErrNoRows when it does not exist.
func (s *Store) GetConfigVersionById(ctx context.Context, id uuid.UUID) (moderationdb.ModerationConfigVersion, error) {
	return moderationdb.New(s.pool).GetConfigVersionById(ctx, id)
}

// GetConfigState returns the singleton config-state row: the active version
// pointer and the activation metadata. pgx.ErrNoRows means the singleton has
// not been created.
func (s *Store) GetConfigState(ctx context.Context) (moderationdb.GetConfigStateRow, error) {
	return moderationdb.New(s.pool).GetConfigState(ctx)
}

// GetActiveConfig returns the active configuration version. ErrNoActiveConfiguration
// is returned when the singleton exists but no version has been activated.
func (s *Store) GetActiveConfig(ctx context.Context) (moderationdb.ModerationConfigVersion, error) {
	version, err := moderationdb.New(s.pool).GetActiveConfigVersionFull(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return moderationdb.ModerationConfigVersion{}, ErrNoActiveConfiguration
	}
	return version, err
}

// ActivateConfigVersion atomically points the active version at versionID and
// records the activating admin in the audit log. The UPDATE is fenced by
// expectedVersionID: nil means no active version is expected; otherwise the
// active version must equal expectedVersionID.
func (s *Store) ActivateConfigVersion(
	ctx context.Context,
	versionID uuid.UUID,
	activatedBy string,
	expectedVersionID *uuid.UUID,
) error {
	return database.Within(ctx, s.pool, func(q *moderationdb.Queries) error {
		rows, err := q.ActivateConfigVersion(ctx, moderationdb.ActivateConfigVersionParams{
			ActiveVersionID:   &versionID,
			ActivatedBy:       &activatedBy,
			ExpectedVersionID: expectedVersionID,
		})
		if err != nil {
			return err
		}
		if rows == 0 {
			return ErrConfigurationConflict
		}
		return q.InsertAdminAudit(ctx, moderationdb.InsertAdminAuditParams{
			Action:          "ACTIVATE_CONFIG",
			ConfigVersionID: &versionID,
			Actor:           activatedBy,
			Detail:          []byte(`{"expected_active_version_id": ` + configVersionIDJSON(expectedVersionID) + `}`),
		})
	})
}

// InsertAudit records one admin action for the audit trail.
func (s *Store) InsertAudit(
	ctx context.Context,
	action string,
	configVersionID *uuid.UUID,
	actor string,
	detail map[string]any,
) error {
	return database.Within(ctx, s.pool, func(q *moderationdb.Queries) error {
		payload, err := encodeAuditDetail(detail)
		if err != nil {
			return err
		}
		return q.InsertAdminAudit(ctx, moderationdb.InsertAdminAuditParams{
			Action:          action,
			ConfigVersionID: configVersionID,
			Actor:           actor,
			Detail:          payload,
		})
	})
}

func configVersionIDJSON(id *uuid.UUID) string {
	if id == nil {
		return "null"
	}
	return `"` + id.String() + `"`
}

func encodeAuditDetail(detail map[string]any) ([]byte, error) {
	if detail == nil {
		return nil, nil
	}
	return json.Marshal(detail)
}
