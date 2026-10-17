# ADR: UUIDv7 for generated entity and event IDs

Date: 16 Oct 2026
Status: accepted

## Context

User and Supplier currently generate random UUIDv4 primary keys through
PostgreSQL's `gen_random_uuid()` and Go's `uuid.New()` / `uuid.NewString()`.
Successive random values can touch unrelated B-tree index pages. UUIDv7's time
prefix groups newly generated values together while retaining distributed ID
generation without a shared sequence.

Compose already uses PostgreSQL 18, which provides `uuidv7()`. The pinned
`github.com/google/uuid` version already provides `NewV7()`.

## Decision

Generate UUIDv7 for new database-backed entity and event IDs:

- Database-generated IDs use `DEFAULT uuidv7()` on PostgreSQL 18 or newer.
- Application-generated IDs use `uuid.NewV7()`. Return construction errors with
  operation context so the surrounding transaction can roll back.
- Generate IDs inside the existing idempotent operation callback. A successful
  retry returns its persisted resource ID instead of creating another identity.
- Keep deterministic Supplier seed IDs as UUIDv5 so importing the same source
  record continues to address the same resource.

This is a generation policy, not a version restriction on UUID inputs. Keep
accepting valid existing IDs, references and client-provided idempotency keys.
Authentication secrets and magic-link tokens retain their existing random-byte
generation; UUIDs are identifiers, not credentials.

## Migration and compatibility

New Goose migrations alter only the defaults on the six UUID-primary-key tables
in each of User and Supplier. Their Down sections restore `gen_random_uuid()`.
Existing rows, foreign keys and UUID column types remain unchanged in either
direction. User's GORM default metadata mirrors the new database default.

Supplier's application paths generate IDs before their explicit-ID inserts, so
those four paths also switch to `NewV7()`. Existing UUIDv4 test references and
idempotency keys remain useful compatibility coverage.

Migration tests exercise fresh installation, upgrade from the previous schema,
rollback and replay. They check the version of generated IDs and preservation
of rows and relationships. Service tests also check application-generated IDs
and repeat identity.

## Consequences

The expected benefit is improved UUID-index locality. We have not measured a
project-specific throughput gain. UUID size and existing query indexes do not
change.

UUIDv7 exposes its generation timestamp. Its ordering does not establish a
global business order across clocks or replace `created_at`, operation
timestamps, revisions or outbox ordinals.

## References

- [RFC 9562: UUIDv7](https://www.rfc-editor.org/rfc/rfc9562.html#section-5.7)
- [PostgreSQL 18 UUID functions](https://www.postgresql.org/docs/18/functions-uuid.html)
- [google/uuid NewV7](https://pkg.go.dev/github.com/google/uuid@v1.6.0#NewV7)
