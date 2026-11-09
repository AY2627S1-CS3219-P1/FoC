package moderation

import (
	"context"
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

// ConfigurationStore persists the immutable configuration versions and the active pointer.
// Writes run inside database.Within; reads run directly against the pool.
type ConfigurationStore struct {
	pool *pgxpool.Pool
}

func NewConfigurationStore(pool *pgxpool.Pool) *ConfigurationStore {
	return &ConfigurationStore{pool: pool}
}

// EnsureConfigState makes the config-state singleton row exist so reads never
// see "no rows" merely because the service has not activated anything yet.
func (s *ConfigurationStore) EnsureConfigState(ctx context.Context) error {
	return database.Within(ctx, s.pool, func(q *moderationdb.Queries) error {
		return q.EnsureConfigStateSingleton(ctx)
	})
}

// InsertConfigVersion persists an immutable configuration version and returns
// its id. It never becomes active; activation is a separate, optimistically
// concurrent step.
func (s *ConfigurationStore) InsertConfigVersion(
	ctx context.Context,
	modelID string,
	parameters []byte,
	outputMode string,
	policyVersion string,
	providerFingerprint string,
	createdBy string,
	systemMessage string,
) (uuid.UUID, error) {
	if systemMessage == "" {
		systemMessage = DefaultSystemMessage()
	}
	var id uuid.UUID
	err := database.Within(ctx, s.pool, func(q *moderationdb.Queries) error {
		var err error
		id, err = q.InsertConfigVersion(ctx, moderationdb.InsertConfigVersionParams{
			SystemMessage:       systemMessage,
			ModelID:             modelID,
			Parameters:          parameters,
			OutputMode:          outputMode,
			PolicyVersion:       policyVersion,
			ProviderFingerprint: providerFingerprint,
			CreatedBy:           createdBy,
		})
		if err != nil {
			return err
		}
		return q.InsertAdminAudit(
			ctx,
			moderationdb.InsertAdminAuditParams{Action: "SAVE_DRAFT", ConfigVersionID: &id, Actor: createdBy},
		)
	})
	return id, err
}

// GetConfigVersionById returns one configuration version by id, or
// pgx.ErrNoRows when it does not exist.
func (s *ConfigurationStore) GetConfigVersionById(
	ctx context.Context,
	id uuid.UUID,
) (moderationdb.ModerationConfigVersion, error) {
	return moderationdb.New(s.pool).GetConfigVersionById(ctx, id)
}

// GetConfigState returns the singleton config-state row: the active version
// pointer and the activation metadata. pgx.ErrNoRows means the singleton has
// not been created.
func (s *ConfigurationStore) GetConfigState(ctx context.Context) (moderationdb.GetConfigStateRow, error) {
	return moderationdb.New(s.pool).GetConfigState(ctx)
}

// GetActiveConfig returns the active configuration version. ErrNoActiveConfiguration
// is returned when the singleton exists but no version has been activated.
func (s *ConfigurationStore) GetActiveConfig(ctx context.Context) (moderationdb.ModerationConfigVersion, error) {
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
func (s *ConfigurationStore) ActivateConfigVersion(
	ctx context.Context,
	versionID uuid.UUID,
	activatedBy string,
	expectedVersionID *uuid.UUID,
) error {
	return s.ActivateAndResume(ctx, versionID, activatedBy, expectedVersionID, false)
}

// ActivateAndResume records explicit admin consent before rebinding paused
func (s *ConfigurationStore) ActivateAndResume(ctx context.Context, versionID uuid.UUID, activatedBy string,
	expectedVersionID *uuid.UUID, resumePausedJobs bool) error {
	return database.Within(ctx, s.pool, func(q *moderationdb.Queries) error {
		return NewTransitions(q).Activate(ctx, versionID, activatedBy, expectedVersionID, resumePausedJobs)
	})
}
