-- name: ListAllowedDomains :many
SELECT id, domain
FROM allowed_email_domains
ORDER BY domain;

-- name: IsDomainAllowed :one
SELECT EXISTS (
    SELECT 1 FROM allowed_email_domains WHERE domain = @domain
);

-- name: AllowDomain :exec
INSERT INTO allowed_email_domains (domain, created_by, updated_by)
VALUES (@domain, @actor_id::UUID, @actor_id::UUID);

-- name: RemoveAllowedDomain :execrows
DELETE FROM allowed_email_domains
WHERE id = @id;
