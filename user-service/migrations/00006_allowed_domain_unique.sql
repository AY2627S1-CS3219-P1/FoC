-- Allowed domains are hard-deleted, so uniqueness applies to every row.
-- +goose Up
ALTER TABLE allowed_email_domains
    ADD CONSTRAINT allowed_email_domains_domain_key UNIQUE (domain);

DROP INDEX idx_allowed_email_domains_live;

-- +goose Down
CREATE UNIQUE INDEX idx_allowed_email_domains_live
    ON allowed_email_domains (domain) WHERE deleted_at IS NULL;

ALTER TABLE allowed_email_domains
    DROP CONSTRAINT allowed_email_domains_domain_key;
