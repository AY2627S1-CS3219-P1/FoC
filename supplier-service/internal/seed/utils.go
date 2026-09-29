package seed

import (
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func resolveSeedID(candidateID, foundID pgtype.UUID, err error) (pgtype.UUID, bool, error) {
	if errors.Is(err, pgx.ErrNoRows) {
		return candidateID, false, nil
	}
	if err != nil {
		return pgtype.UUID{}, false, err
	}
	return foundID, true, nil
}

func recordSeedChange(counts *Counts, existed bool, changed int64) {
	if !existed {
		counts.Inserted++
	} else if changed > 0 {
		counts.Updated++
	}
}

func deterministicUUID(sourceKey string) pgtype.UUID {
	id := uuid.NewSHA1(seedNamespace, []byte(sourceKey))
	return pgtype.UUID{Bytes: id, Valid: true}
}
